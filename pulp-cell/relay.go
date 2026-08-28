package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BananaLabs-OSS/Fiber/pulp/udp"
	"github.com/BananaLabs-OSS/Fiber/pulp/workflow"
)

// PlayerSession tracks the per-player outbound socket and target binding.
// Each session has a dedicated udp.Socket for target->relay->player
// traffic; the OnPacket callback on that socket forwards replies to the
// player through the shared inbound socket.
type DatagramSession struct {
	RouteKey       string
	SourceEndpoint string // full source endpoint
	Target         string // opaque UDP target
	OutboundSock   *udp.Socket
	LastActivity   uint64 // wall-time nanoseconds
}

// Relay owns the inbound UDP socket, the routing table, and the set of
// per-player sessions. All state is plain maps — WASM is single-threaded.
type Relay struct {
	listenAddr       string
	bufferSize       int
	idleTimeout      time.Duration
	routeEvent       string
	routeResolverURL string
	orchestrator     *workflow.Client

	state       StateClient
	inboundSock *udp.Socket

	// player endpoint (ip:port) -> session. NAT gives simultaneous players
	// behind one public IP distinct source ports, so the endpoint—not the
	// host-only route key—is the live session identity.
	sessions map[string]*DatagramSession

	// negativeCache maps routeKey → expiry wall-time (nanoseconds).
	// An IP present here with expiry in the future means a recent
	// requestRoute failed; skip the HTTP call for 30s to avoid
	// blocking the step loop on every junk packet from that IP.
}

// New constructs an unstarted relay. Call Start to bind the inbound
// socket and wire the packet callback.
func New(listenAddr string, bufferSize int, idleTimeout time.Duration, routeEvent, routeResolverURL string) *Relay {
	return &Relay{
		listenAddr:       listenAddr,
		bufferSize:       bufferSize,
		idleTimeout:      idleTimeout,
		routeEvent:       routeEvent,
		routeResolverURL: routeResolverURL,
		orchestrator:     workflow.NewClient("lua-orchestrator"),
		sessions:         make(map[string]*DatagramSession),
	}
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
// and forwards the packet to the target via the session's outbound
// socket.
func (r *Relay) onInbound(pkt udp.Packet) {
	sessionKey := "udp:" + pkt.SrcAddr
	sess, exists := r.sessions[sessionKey]
	if !exists {
		routeKey, target, err := r.resolveRoute(pkt.SrcAddr, sessionKey)
		if err != nil {
			log.Printf("Route resolution failed for %s: %v", sessionKey, err)
			return
		}
		sess, err = r.getOrCreateSession(sessionKey, routeKey, pkt.SrcAddr, target, pkt.ReceivedAt)
		if err != nil {
			log.Printf("Session error for %s: %v", sessionKey, err)
			return
		}
	}

	// Remember the most-recent source addr so replies land on the right
	// ephemeral port.
	sess.SourceEndpoint = pkt.SrcAddr
	sess.LastActivity = uint64(pkt.ReceivedAt)
	if err := r.state.SessionTouch(
		fmt.Sprintf("session-touch:%s:%d", sessionKey, pkt.ReceivedAt),
		sessionKey, sess.RouteKey, pkt.SrcAddr, sess.Target, fmt.Sprint(pkt.ReceivedAt),
	); err != nil {
		log.Printf("Session error for %s: %v", sessionKey, err)
		return
	}

	// Native calls WriteToUDP without checking its error — packet drops
	// are silent. Cell matches that: host-side send failures are
	// already logged by Pulp-ext-udp at source, so double-logging here
	// would be noise parity tests could trip on.
	_, _ = sess.OutboundSock.Send(sess.Target, pkt.Payload)
}

// getOrCreateSession returns the existing session for the exact player
// endpoint or synchronously opens a new outbound socket and wires its callback.
// Creating a socket in the step loop is acceptable — it's a single
// host call, no goroutines, no sleeps.
//
// Does NOT check for target mismatch — matches native Peel's explicit
// "Don't check target mismatch" comment. The only way to change a
// session's target is via UpdateRouteTarget, which callers invoke
// from the HTTP setRoute handler before the next packet arrives.
func (r *Relay) getOrCreateSession(sessionKey, routeKey, sourceEndpoint, target string, now int64) (*DatagramSession, error) {
	if sess, ok := r.sessions[sessionKey]; ok {
		return sess, nil
	}

	outbound, err := udp.Listen("", r.bufferSize) // ephemeral local port
	if err != nil {
		return nil, fmt.Errorf("outbound udp listen: %w", err)
	}

	sess := &DatagramSession{
		RouteKey:       routeKey,
		SourceEndpoint: sourceEndpoint,
		Target:         target,
		OutboundSock:   outbound,
		LastActivity:   uint64(now),
	}

	// The outbound socket's packet callback carries target responses back to
	// the exact source endpoint that created this flow.
	key := sessionKey
	outbound.OnPacket(func(pkt udp.Packet) {
		cur, ok := r.sessions[key]
		if !ok {
			return
		}
		cur.LastActivity = uint64(pkt.ReceivedAt)
		if err := r.state.SessionTouch(
			fmt.Sprintf("session-reply:%s:%d", key, pkt.ReceivedAt),
			key, cur.RouteKey, cur.SourceEndpoint, cur.Target, fmt.Sprint(pkt.ReceivedAt),
		); err != nil {
			log.Printf("Session error for %s: %v", key, err)
			return
		}
		// Match native: no error logging on reply write — native's
		// readTargetResponses does not check WriteToUDP's return.
		_, _ = r.inboundSock.Send(cur.SourceEndpoint, pkt.Payload)
	})

	r.sessions[sessionKey] = sess
	log.Printf("Session created: %s → %s", sessionKey, target)
	return sess, nil
}

// closeSessionLocked is the inner close — identical to CloseGroup
// but named to match the Go-stdlib convention for the no-lock variant.
// In WASM there is no lock, but the naming signals intent.
//
// Native CloseGroup ignores OutboundConn.Close's return; we do the
// same so no cell-only log line can diverge from native output.
func (r *Relay) closeSessionLocked(sessionKey string) {
	sess, ok := r.sessions[sessionKey]
	if !ok {
		return
	}
	_ = sess.OutboundSock.Close()
	delete(r.sessions, sessionKey)
	log.Printf("Session closed: %s", sessionKey)
}

// SweepIdle runs once per step. It bounds two cell-only maps: it drops
// expired negativeCache entries, and closes sessions that have been silent
// for longer than idleTimeout — dropping each idle session's auto-resolved
// route with it so the route table can't grow one entry per unique player
// IP without ever shrinking.
func (r *Relay) SweepIdle(wallNanos uint64) {
	// Bound the negative cache independently of idleTimeout: an entry is
	// only ever removed on a later successful lookup for the same IP
	// (relay.go onInbound), so without this sweep a failing or spoofed
	// source IP that never returns leaks a slot forever. Expiry was stamped
	// with time.Now().UnixNano(); read the same clock here to compare in the
	// same domain rather than assuming ev.WallTime shares that epoch.
	idleNanos := uint64(0)
	if r.idleTimeout > 0 {
		idleNanos = uint64(r.idleTimeout)
	}
	closed, err := r.state.Sweep(
		fmt.Sprintf("sweep:%d", wallNanos),
		fmt.Sprint(wallNanos), fmt.Sprint(idleNanos),
	)
	if err != nil {
		log.Printf("Session sweep failed: %v", err)
		return
	}
	for _, sessionKey := range closed {
		r.closeSessionLocked(sessionKey)
		// Drop the auto-resolved route too. A route created on the first
		// packet (onInbound) is otherwise never removed once its player
		// goes idle — the leak-by-construction this sweep exists to
		// prevent. A returning player simply re-resolves on next packet.
		// API-managed routes (POST /routes) have no session and are not
		// touched here; their lifecycle is owned by Bananasplit's
		// DELETE /routes call, matching deleteRoute's explicit teardown.
	}
	r.reconcileDesiredState()
}

func (r *Relay) reconcileDesiredState() {
	snapshot, err := r.state.Snapshot()
	if err != nil {
		log.Printf("State reconciliation failed: %v", err)
		return
	}
	for sessionKey, session := range r.sessions {
		durable, exists := snapshot.Sessions[sessionKey]
		target, routed := snapshot.Routes[session.RouteKey]
		if !exists || durable.Key != session.RouteKey || durable.Target != session.Target || !routed || target != session.Target {
			r.closeSessionLocked(sessionKey)
		}
	}
}

func (r *Relay) resolveRoute(sourceEndpoint, sessionKey string) (string, string, error) {
	result, err := r.orchestrator.Dispatch(workflow.DispatchRequest{
		Event: r.routeEvent,
		Payload: map[string]any{
			"source_endpoint": sourceEndpoint,
			"session_key":     sessionKey,
			"now_millis":      time.Now().UnixMilli(),
			"resolver_url":    r.routeResolverURL,
		},
	})
	if err != nil {
		return "", "", err
	}
	value, err := workflow.DecodeValue[struct {
		Key    string `msgpack:"key"`
		Target string `msgpack:"target"`
	}](result)
	if err != nil || value.Key == "" || value.Target == "" {
		return "", "", fmt.Errorf("composition returned no route: %w", err)
	}
	return value.Key, value.Target, nil
}

// Stop tears down every session and closes the inbound socket. Intended
// for OnShutdown — idempotent.
//
// Matches native Peel's Stop order: drain sessions (close each outbound
// socket) → close inbound. Native also closes a `quit` chan first to
// signal the hot read loop; WASM has no hot loop (packets are step-driven)
// so that step is elided. Neither native nor cell emits the per-session
// "session closed" log here — native bypasses CloseGroup and cell
// mirrors that by calling OutboundSock.Close directly.
func (r *Relay) Stop() {
	for _, sess := range r.sessions {
		_ = sess.OutboundSock.Close()
	}
	r.sessions = make(map[string]*DatagramSession)
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
