package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/BananaLabs-OSS/Fiber/pulp/udp"
)

// PlayerSession tracks the per-player outbound socket and backend binding.
// Each session has a dedicated udp.Socket for backend->relay->player
// traffic; the OnPacket callback on that socket forwards replies to the
// player through the shared inbound socket.
type PlayerSession struct {
	PlayerAddr   string // "ip:port" — full source addr of last inbound packet
	Backend      string // "host:port" — backend target
	OutboundSock *udp.Socket
	LastActivity uint64 // wall-time nanoseconds
}

// Relay owns the inbound UDP socket, the routing table, and the set of
// per-player sessions. All state is plain maps — WASM is single-threaded.
type Relay struct {
	listenAddr     string
	bananasplitURL string
	bufferSize     int
	idleTimeout    time.Duration

	router      *Router
	inboundSock *udp.Socket

	// playerIP -> session. Key is the host portion of PlayerAddr, to
	// match the route map which is keyed by IP only.
	sessions map[string]*PlayerSession
}

// New constructs an unstarted relay. Call Start to bind the inbound
// socket and wire the packet callback.
func New(listenAddr, bananasplitURL string, bufferSize int, idleTimeout time.Duration) *Relay {
	return &Relay{
		listenAddr:     listenAddr,
		bananasplitURL: bananasplitURL,
		bufferSize:     bufferSize,
		idleTimeout:    idleTimeout,
		router:         NewRouter(),
		sessions:       make(map[string]*PlayerSession),
	}
}

// Router exposes the underlying route table so the HTTP API can manage
// entries directly.
func (r *Relay) Router() *Router {
	return r.router
}

// Start binds the inbound UDP socket and registers its packet handler.
// Must be called from OnInit (before OnStep fires).
func (r *Relay) Start() error {
	sock, err := udp.Listen(r.listenAddr, r.bufferSize)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", r.listenAddr, err)
	}
	r.inboundSock = sock

	sock.OnPacket(r.onInbound)

	log.Printf("UDP relay listening on %s", r.listenAddr)
	return nil
}

// onInbound runs for every datagram received on the inbound socket
// (player -> relay). Looks up the route, finds or creates a session,
// and forwards the packet to the backend via the session's outbound
// socket.
func (r *Relay) onInbound(pkt udp.Packet) {
	playerIP := hostOf(pkt.SrcAddr)

	backend, hasRoute := r.router.Get(playerIP)
	if !hasRoute {
		// No route cached. Ask Bananasplit synchronously. This blocks
		// the step loop for the duration of the HTTP call — acceptable
		// since this only happens for the first packet of a session.
		resolved, err := r.requestRoute(playerIP)
		if err != nil {
			log.Printf("Failed to get route for %s: %v", playerIP, err)
			return
		}
		r.router.Set(playerIP, resolved)
		backend = resolved
	}

	sess, err := r.getOrCreateSession(playerIP, pkt.SrcAddr, backend, pkt.ReceivedAt)
	if err != nil {
		log.Printf("Session error for %s: %v", playerIP, err)
		return
	}

	// Remember the most-recent source addr so replies land on the right
	// ephemeral port.
	sess.PlayerAddr = pkt.SrcAddr
	sess.LastActivity = uint64(pkt.ReceivedAt)

	// Native calls WriteToUDP without checking its error — packet drops
	// are silent. Cell matches that: host-side send failures are
	// already logged by Pulp-ext-udp at source, so double-logging here
	// would be noise parity tests could trip on.
	_, _ = sess.OutboundSock.Send(sess.Backend, pkt.Payload)
}

// getOrCreateSession returns the existing session for playerIP or
// synchronously opens a new outbound socket and wires its callback.
// Creating a socket in the step loop is acceptable — it's a single
// host call, no goroutines, no sleeps.
//
// Does NOT check for backend mismatch — matches native Peel's explicit
// "Don't check backend mismatch" comment. The only way to change a
// session's backend is via UpdateSessionBackend, which callers invoke
// from the HTTP setRoute handler before the next packet arrives.
func (r *Relay) getOrCreateSession(playerIP, playerAddr, backend string, now int64) (*PlayerSession, error) {
	if sess, ok := r.sessions[playerIP]; ok {
		return sess, nil
	}

	outbound, err := udp.Listen("", r.bufferSize) // ephemeral local port
	if err != nil {
		return nil, fmt.Errorf("outbound udp listen: %w", err)
	}

	sess := &PlayerSession{
		PlayerAddr:   playerAddr,
		Backend:      backend,
		OutboundSock: outbound,
		LastActivity: uint64(now),
	}

	// The outbound socket's packet callback carries backend responses
	// back to the player. We bind playerIP (not the captured PlayerAddr)
	// because the player's ephemeral port may change — always read the
	// current PlayerAddr from the session map at reply time.
	ip := playerIP
	outbound.OnPacket(func(pkt udp.Packet) {
		cur, ok := r.sessions[ip]
		if !ok {
			return
		}
		cur.LastActivity = uint64(pkt.ReceivedAt)
		// Match native: no error logging on reply write — native's
		// readBackendResponses does not check WriteToUDP's return.
		_, _ = r.inboundSock.Send(cur.PlayerAddr, pkt.Payload)
	})

	r.sessions[playerIP] = sess
	log.Printf("Session created: %s → %s", playerIP, backend)
	return sess, nil
}

// UpdateSessionBackend closes the current session for playerIP and
// updates the route. The next packet from that player will create a
// new session bound to newBackend.
//
// Mirrors native Peel's UpdateSessionBackend: only acts when a session
// exists for playerIP, and only after the new backend passes a basic
// address validation (native uses net.ResolveUDPAddr; the cell has no
// resolver in-WASM so it does a syntactic host:port check instead).
// Rejecting malformed input here matches the native short-circuit and
// keeps the stored route consistent with what was previously set on
// Router.Set by the caller.
//
// Side-effect order matches native: close session (which logs "session
// closed") → log "session backend updated" → Router.Set. The final
// Router.Set is redundant with the caller's earlier Router.Set but is
// preserved for byte-parity of side-effect ordering.
func (r *Relay) UpdateSessionBackend(playerIP, newBackend string) {
	if _, ok := r.sessions[playerIP]; !ok {
		return
	}
	if !validBackendAddr(newBackend) {
		return
	}
	r.closeSessionLocked(playerIP)
	log.Printf("Session backend updated: %s → %s", playerIP, newBackend)
	r.router.Set(playerIP, newBackend)
}

// validBackendAddr returns true when addr parses as host:port — the
// WASM-side stand-in for net.ResolveUDPAddr which the cell can't call
// (no net package in wasip1 without pulp.UDP). Accepts IPv4, IPv6 in
// brackets, hostnames, and the ":port" short form (empty host, which
// net.ResolveUDPAddr treats as 0.0.0.0).
func validBackendAddr(addr string) bool {
	if addr == "" {
		return false
	}
	// IPv6 literal: "[::1]:5520"
	if strings.HasPrefix(addr, "[") {
		end := strings.Index(addr, "]")
		if end < 0 || end+1 >= len(addr) || addr[end+1] != ':' {
			return false
		}
		return isPort(addr[end+2:])
	}
	i := strings.LastIndex(addr, ":")
	// i == 0 is the ":port" form — native's net.ResolveUDPAddr accepts
	// it as host=0.0.0.0. i == len-1 means "host:" with no port, which
	// native rejects.
	if i < 0 || i == len(addr)-1 {
		return false
	}
	return isPort(addr[i+1:])
}

// isPort returns true when s is a 1-5 digit decimal integer in [1,65535].
func isPort(s string) bool {
	if len(s) == 0 || len(s) > 5 {
		return false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
		if n > 65535 {
			return false
		}
	}
	return n > 0
}

// CloseSession drops the session for playerIP and tears down its
// outbound socket. Safe to call for an unknown playerIP.
func (r *Relay) CloseSession(playerIP string) {
	r.closeSessionLocked(playerIP)
}

// closeSessionLocked is the inner close — identical to CloseSession
// but named to match the Go-stdlib convention for the no-lock variant.
// In WASM there is no lock, but the naming signals intent.
//
// Native CloseSession ignores OutboundConn.Close's return; we do the
// same so no cell-only log line can diverge from native output.
func (r *Relay) closeSessionLocked(playerIP string) {
	sess, ok := r.sessions[playerIP]
	if !ok {
		return
	}
	_ = sess.OutboundSock.Close()
	delete(r.sessions, playerIP)
	log.Printf("Session closed: %s", playerIP)
}

// SweepIdle runs once per step. Closes sessions that have been silent
// for longer than idleTimeout.
func (r *Relay) SweepIdle(wallNanos uint64) {
	if r.idleTimeout <= 0 {
		return
	}
	cutoff := uint64(r.idleTimeout)
	for ip, sess := range r.sessions {
		if wallNanos > sess.LastActivity && wallNanos-sess.LastActivity > cutoff {
			r.closeSessionLocked(ip)
		}
	}
}

// requestRoute asks Bananasplit for the backend that should serve
// playerIP. Synchronous outbound HTTP via the pulp transport.
func (r *Relay) requestRoute(playerIP string) (string, error) {
	if r.bananasplitURL == "" {
		return "", fmt.Errorf("bananasplit_url not configured")
	}

	body, _ := json.Marshal(map[string]string{"player_ip": playerIP})

	// 5s budget: this call blocks the step loop, so a slow or dead
	// Bananasplit must not stall packet forwarding. Fail fast, log, and
	// drop the packet — the player's client will resend and a later
	// fetch attempt will re-hit Bananasplit.
	resp, err := pulp.HTTP.Fetch(pulp.HTTPFetchRequest{
		Method:  "POST",
		URL:     r.bananasplitURL + "/route-request",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("route request: %w", err)
	}
	if resp.Status != 200 {
		return "", fmt.Errorf("route request failed: %d %s", resp.Status, resp.Body)
	}

	var parsed struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return "", fmt.Errorf("decode route response: %w", err)
	}
	if parsed.Backend == "" {
		return "", fmt.Errorf("empty backend in route response")
	}

	log.Printf("Route assigned: %s -> %s", playerIP, parsed.Backend)
	return parsed.Backend, nil
}

// Stop tears down every session and closes the inbound socket. Intended
// for OnShutdown — idempotent.
//
// Matches native Peel's Stop order: drain sessions (close each outbound
// socket) → close inbound. Native also closes a `quit` chan first to
// signal the hot read loop; WASM has no hot loop (packets are step-driven)
// so that step is elided. Neither native nor cell emits the per-session
// "session closed" log here — native bypasses CloseSession and cell
// mirrors that by calling OutboundSock.Close directly.
func (r *Relay) Stop() {
	for _, sess := range r.sessions {
		_ = sess.OutboundSock.Close()
	}
	r.sessions = make(map[string]*PlayerSession)
	if r.inboundSock != nil {
		_ = r.inboundSock.Close()
		r.inboundSock = nil
	}
}

// hostOf returns the host portion of an "ip:port" or "[ipv6]:port"
// address. Falls back to the whole string when no port is present.
func hostOf(addr string) string {
	// IPv6: "[::1]:5520"
	if strings.HasPrefix(addr, "[") {
		if end := strings.Index(addr, "]"); end >= 0 {
			return addr[1:end]
		}
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

