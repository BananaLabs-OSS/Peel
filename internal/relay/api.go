package relay

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
)

type API struct {
	relay        *Relay
	addr         string
	serviceToken string
}

func NewAPI(relay *Relay, addr string) *API {
	token := os.Getenv("SERVICE_TOKEN")
	if token == "" {
		log.Printf("WARNING: SERVICE_TOKEN is not set — /routes endpoint is unauthenticated")
	}
	return &API{
		relay:        relay,
		addr:         addr,
		serviceToken: token,
	}
}

// checkServiceToken verifies the X-Service-Token header when SERVICE_TOKEN
// is configured. Returns true if the request is allowed.
func (a *API) checkServiceToken(w http.ResponseWriter, r *http.Request) bool {
	if a.serviceToken == "" {
		// No token configured — allow (backward compat).
		return true
	}
	if r.Header.Get("X-Service-Token") != a.serviceToken {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func (a *API) Start() error {
	http.HandleFunc("POST /routes", a.setRoute)
	http.HandleFunc("DELETE /routes/{playerIP}", a.deleteRoute)
	http.HandleFunc("DELETE /sessions/{playerIP}", a.closeSession)
	http.HandleFunc("GET /routes", a.listRoutes)
	http.HandleFunc("GET /health", a.health)

	return http.ListenAndServe(a.addr, nil)
}

// POST /routes
// {"player_ip": "203.0.113.50", "backend": "10.0.50.2:5521"}
func (a *API) setRoute(w http.ResponseWriter, r *http.Request) {
	if !a.checkServiceToken(w, r) {
		return
	}

	var req struct {
		PlayerIP string `json:"player_ip"`
		Backend  string `json:"backend"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.PlayerIP == "" || req.Backend == "" {
		http.Error(w, "player_ip and backend required", http.StatusBadRequest)
		return
	}

	if _, err := net.ResolveUDPAddr("udp", req.Backend); err != nil {
		http.Error(w, `{"error":"invalid backend address"}`, http.StatusBadRequest)
		return
	}

	oldBackend, hadRoute := a.relay.Router().Get(req.PlayerIP)
	a.relay.Router().Set(req.PlayerIP, req.Backend)

	if hadRoute && oldBackend != req.Backend {
		a.relay.UpdateSessionBackend(req.PlayerIP, req.Backend)
		log.Printf("Route changed: %s %s → %s", req.PlayerIP, oldBackend, req.Backend)
	} else {
		log.Printf("Route set: %s → %s", req.PlayerIP, req.Backend)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// DELETE /routes/{playerIP}
func (a *API) deleteRoute(w http.ResponseWriter, r *http.Request) {
	if !a.checkServiceToken(w, r) {
		return
	}
	playerIP := r.PathValue("playerIP")
	if playerIP == "" {
		http.Error(w, "player_ip required", http.StatusBadRequest)
		return
	}

	a.relay.Router().Delete(playerIP)
	a.relay.CloseSession(playerIP)
	log.Printf("Route deleted: %s", playerIP)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// DELETE /sessions/{playerIP}
func (a *API) closeSession(w http.ResponseWriter, r *http.Request) {
	if !a.checkServiceToken(w, r) {
		return
	}
	playerIP := r.PathValue("playerIP")
	if playerIP == "" {
		http.Error(w, "player_ip required", http.StatusBadRequest)
		return
	}

	a.relay.CloseSession(playerIP)
	log.Printf("Session closed via API: %s", playerIP)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// GET /routes
func (a *API) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes := a.relay.Router().List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(routes)
}

// GET /health
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}
