package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type PlayerSession struct {
	PlayerAddr   *net.UDPAddr
	Backend      string
	BackendAddr  *net.UDPAddr
	OutboundConn *net.UDPConn
	quit         chan struct{}
}

type Relay struct {
	listenAddr     string
	defaultBackend string
	router         *Router
	inboundConn    *net.UDPConn

	// Reverse mapping: backendAddr+playerPort → playerAddr
	// So we know where to send responses
	sessions sync.Map // playerIP → *PlayerSession

	bananasplitURL string
	httpClient     *http.Client
	bufferSize     int

	quit chan struct{}
}

func New(listenAddr, defaultBackend string, bananasplitURL string, bufferSize int) *Relay {
	return &Relay{
		listenAddr:     listenAddr,
		defaultBackend: defaultBackend,
		router:         NewRouter(),
		quit:           make(chan struct{}),
		bananasplitURL: bananasplitURL,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		bufferSize:     bufferSize,
	}
}

func (r *Relay) Router() *Router {
	return r.router
}

func (r *Relay) Start() error {
	addr, err := net.ResolveUDPAddr("udp", r.listenAddr)
	if err != nil {
		return err
	}

	r.inboundConn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	r.inboundConn.SetReadBuffer(r.bufferSize)

	log.Printf("UDP relay listening on %s", r.listenAddr)

	buf := make([]byte, 65535) // Max UDP packet size

	for {
		select {
		case <-r.quit:
			return nil
		default:
			n, playerAddr, err := r.inboundConn.ReadFromUDP(buf)
			if err != nil {
				continue
			}

			data := make([]byte, n)
			copy(data, buf[:n])
			go r.handlePacket(data, playerAddr)
		}
	}
}

func (r *Relay) handlePacket(data []byte, fromAddr *net.UDPAddr) {
	fromIP := fromAddr.IP.String()

	backend, hasRoute := r.router.Get(fromIP)
	if !hasRoute {
		var err error
		backend, err = r.requestRoute(fromIP)
		if err != nil {
			log.Printf("Failed to get route for %s: %v", fromIP, err)
			return
		}
		r.router.Set(fromIP, backend)
	}

	r.forwardToBackend(data, fromAddr, backend)
}

func (r *Relay) getOrCreateSession(playerAddr *net.UDPAddr, backend string) (*PlayerSession, error) {
	playerIP := playerAddr.IP.String()

	if val, ok := r.sessions.Load(playerIP); ok {
		session := val.(*PlayerSession)
		// Don't check backend mismatch - let timer handle it
		session.PlayerAddr = playerAddr
		return session, nil
	}

	outbound, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}

	outbound.SetReadBuffer(r.bufferSize)

	backendAddr, err := net.ResolveUDPAddr("udp", backend)
	if err != nil {
		outbound.Close()
		return nil, err
	}

	session := &PlayerSession{
		PlayerAddr:   playerAddr,
		Backend:      backend,
		BackendAddr:  backendAddr,
		OutboundConn: outbound,
		quit:         make(chan struct{}),
	}

	actual, loaded := r.sessions.LoadOrStore(playerIP, session)
	if loaded {
		// Someone else created it first, clean up ours
		outbound.Close()
		return actual.(*PlayerSession), nil
	}

	go r.readBackendResponses(session, playerIP)
	log.Printf("Session created: %s → %s", playerIP, backend)
	return session, nil
}

func (r *Relay) UpdateSessionBackend(playerIP, newBackend string) {
	if val, ok := r.sessions.Load(playerIP); ok {
		session := val.(*PlayerSession)
		newAddr, err := net.ResolveUDPAddr("udp", newBackend)
		if err == nil {
			session.Backend = newBackend
			session.BackendAddr = newAddr
			log.Printf("Session backend updated: %s → %s", playerIP, newBackend)
		}
	}
}

func (r *Relay) readBackendResponses(session *PlayerSession, playerIP string) {
	buf := make([]byte, 65535)

	for {
		select {
		case <-session.quit:
			return
		default:
			session.OutboundConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := session.OutboundConn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}
			r.inboundConn.WriteToUDP(buf[:n], session.PlayerAddr)
		}
	}
}

func (r *Relay) CloseSession(playerIP string) {
	if val, ok := r.sessions.LoadAndDelete(playerIP); ok {
		session := val.(*PlayerSession)
		close(session.quit)
		session.OutboundConn.Close()
		log.Printf("Session closed: %s", playerIP)
	}
}

func (r *Relay) requestRoute(playerIP string) (string, error) {
	if r.bananasplitURL == "" {
		return "", fmt.Errorf("BANANASPLIT_URL not configured")
	}

	body, _ := json.Marshal(map[string]string{
		"player_ip": playerIP,
	})

	resp, err := r.httpClient.Post(r.bananasplitURL+"/route-request", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("route request failed: %d", resp.StatusCode)
	}

	var result struct {
		Backend string `json:"backend"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	log.Printf("Route assigned: %s -> %s", playerIP, result.Backend)
	return result.Backend, nil
}

func (r *Relay) forwardToBackend(data []byte, playerAddr *net.UDPAddr, backend string) {
	session, err := r.getOrCreateSession(playerAddr, backend)
	if err != nil {
		log.Printf("Session error for %s: %v", playerAddr.IP.String(), err)
		return
	}

	session.OutboundConn.WriteToUDP(data, session.BackendAddr)
}

func (r *Relay) Stop() {
	close(r.quit)

	r.sessions.Range(func(key, value any) bool {
		session := value.(*PlayerSession)
		close(session.quit)
		session.OutboundConn.Close()
		return true
	})

	if r.inboundConn != nil {
		r.inboundConn.Close()
	}
}
