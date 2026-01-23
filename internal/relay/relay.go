package relay

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Relay struct {
	listenAddr     string
	defaultBackend string
	router         *Router
	conn           *net.UDPConn

	// Reverse mapping: backendAddr+playerPort → playerAddr
	// So we know where to send responses
	reverseMap sync.Map

	bananasplitURL string
	httpClient     *http.Client

	quit chan struct{}
}

func New(listenAddr, defaultBackend string, bananasplitURL string) *Relay {
	return &Relay{
		listenAddr:     listenAddr,
		defaultBackend: defaultBackend,
		router:         NewRouter(),
		quit:           make(chan struct{}),
		bananasplitURL: bananasplitURL,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
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

	r.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}

	log.Printf("UDP relay listening on %s", r.listenAddr)

	buf := make([]byte, 65535) // Max UDP packet size

	for {
		select {
		case <-r.quit:
			return nil
		default:
			n, playerAddr, err := r.conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}

			go r.handlePacket(buf[:n], playerAddr)
		}
	}
}

func (r *Relay) handlePacket(data []byte, fromAddr *net.UDPAddr) {
	fromIP := fromAddr.IP.String()

	// Check if this is from a player (has a route)
	backend, isPlayer := r.router.Get(fromIP)

	if isPlayer {
		r.forwardToBackend(data, fromAddr, backend)
		return
	}

	// Check if this is from a backend (has a reverse mapping)
	if r.isBackend(fromAddr) {
		r.forwardToPlayer(data, fromAddr)
		return
	}

	// Unknown IP - new player, need to assign
	backend, err := r.assignPlayer(fromIP)
	if err != nil {
		log.Printf("Failed to assign %s: %v", fromIP, err)
		return
	}

	// Set route and forward
	r.router.Set(fromIP, backend)
	r.forwardToBackend(data, fromAddr, backend)
}

func (r *Relay) isBackend(addr *net.UDPAddr) bool {
	// Check if this address is in our reverse map (known backend)
	_, exists := r.reverseMap.Load(addr.String())
	return exists
}

func (r *Relay) assignPlayer(playerIP string) (string, error) {
	if r.bananasplitURL == "" {
		return "", fmt.Errorf("no bananasplit URL configured")
	}

	url := fmt.Sprintf("%s/assign?ip=%s", r.bananasplitURL, playerIP)

	resp, err := r.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("assign request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("assign returned %d", resp.StatusCode)
	}

	var result struct {
		Backend string `json:"backend"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode failed: %w", err)
	}

	log.Printf("Assigned %s → %s", playerIP, result.Backend)
	return result.Backend, nil
}

func (r *Relay) forwardToBackend(data []byte, playerAddr *net.UDPAddr, backend string) {
	backendAddr, err := net.ResolveUDPAddr("udp", backend)
	if err != nil {
		log.Printf("Invalid backend address %s: %v", backend, err)
		return
	}

	// Store reverse mapping: backend → player
	r.reverseMap.Store(backendAddr.String(), playerAddr)

	_, err = r.conn.WriteToUDP(data, backendAddr)
	if err != nil {
		log.Printf("Failed to forward to backend %s: %v", backend, err)
	}
}

func (r *Relay) forwardToPlayer(data []byte, backendAddr *net.UDPAddr) {
	// Look up which player this backend is talking to
	playerAddr, ok := r.reverseMap.Load(backendAddr.String())
	if !ok {
		log.Printf("No player mapping for backend %s", backendAddr.String())
		return
	}

	_, err := r.conn.WriteToUDP(data, playerAddr.(*net.UDPAddr))
	if err != nil {
		log.Printf("Failed to forward to player: %v", err)
	}
}

func (r *Relay) Stop() {
	close(r.quit)
	if r.conn != nil {
		r.conn.Close()
	}
}
