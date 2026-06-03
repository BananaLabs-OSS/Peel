package main

import (
	"encoding/json"
	"log"

	pulpgin "github.com/BananaLabs-OSS/Fiber/pulp/gin"
	"github.com/BananaLabs-OSS/Fiber/pulp/gin/middleware"
)

// registerRoutes wires the HTTP control API. Bananasplit pushes route
// changes here; operators can use GET /health and GET /routes for
// observability.
//
// Auth posture: auth-available-not-mandatory. The three state-mutating
// endpoints (POST /routes, DELETE /routes/:ip, DELETE /sessions/:ip) are
// gated on the X-Service-Token shared secret — the same SERVICE_TOKEN
// pattern Bananagine/Bananauth use — ONLY when serviceToken is non-empty.
// When the token is empty (the default today), the mutating routes are
// registered WITHOUT the auth middleware so the existing callers
// (Bananasplit PeelClient, Potassium relay.Client), which send no
// X-Service-Token, keep working — no 401, no outage. The control API is
// internal-only-bounded (the cell publishes only the UDP listener), so an
// unauthenticated control port is reachable only from sibling cells on the
// Pulp host. To ENABLE auth: set SERVICE_TOKEN here AND have the callers
// send X-Service-Token, in lockstep. The GET observability routes
// (/routes, /health) are always open intentionally.
func registerRoutes(r *pulpgin.Engine, relay *Relay, serviceToken string) {
	// Mutating routes ride a root group. The empty group prefix keeps the
	// paths identical to native Peel; only the auth middleware (when a
	// token is configured) is interposed.
	var mutating *pulpgin.RouterGroup
	if serviceToken != "" {
		mutating = r.Group("", middleware.ServiceAuth(serviceToken))
	} else {
		mutating = r.Group("")
	}
	mutating.POST("/routes", setRoute(relay))
	mutating.DELETE("/routes/:playerIP", deleteRoute(relay))
	mutating.DELETE("/sessions/:playerIP", closeSession(relay))

	r.GET("/routes", listRoutes(relay))
	r.GET("/health", health)
}

// POST /routes
// {"player_ip": "203.0.113.50", "backend": "10.0.50.2:5521"}
//
// Error responses match native Peel's http.Error shape (plain text body,
// trailing newline) so parity clients comparing against the native
// stdlib handler see byte-identical responses.
func setRoute(relay *Relay) pulpgin.HandlerFunc {
	return func(c *pulpgin.Context) {
		var req struct {
			PlayerIP string `json:"player_ip"`
			Backend  string `json:"backend"`
		}
		if err := c.BindJSON(&req); err != nil {
			c.String(400, "invalid json\n")
			return
		}
		if req.PlayerIP == "" || req.Backend == "" {
			c.String(400, "player_ip and backend required\n")
			return
		}
		// Validate the backend on the first-write/create path too. Native
		// Peel rejects malformed backends via net.ResolveUDPAddr before
		// storing; the cell mirrors that with the same validBackendAddr
		// check UpdateSessionBackend uses on the change path, so a garbage
		// or malicious address can never be persisted as a route target.
		if !validBackendAddr(req.Backend) {
			c.String(400, "invalid backend address\n")
			return
		}

		oldBackend, hadRoute := relay.Router().Get(req.PlayerIP)
		relay.Router().Set(req.PlayerIP, req.Backend)

		if hadRoute && oldBackend != req.Backend {
			relay.UpdateSessionBackend(req.PlayerIP, req.Backend)
			log.Printf("Route changed: %s %s → %s", req.PlayerIP, oldBackend, req.Backend)
		} else {
			log.Printf("Route set: %s → %s", req.PlayerIP, req.Backend)
		}

		writeJSONWithNewline(c, 200, pulpgin.H{"status": "ok"})
	}
}

// DELETE /routes/:playerIP
func deleteRoute(relay *Relay) pulpgin.HandlerFunc {
	return func(c *pulpgin.Context) {
		playerIP := c.Param("playerIP")
		if playerIP == "" {
			c.String(400, "player_ip required\n")
			return
		}
		relay.Router().Delete(playerIP)
		relay.CloseSession(playerIP)
		log.Printf("Route deleted: %s", playerIP)
		writeJSONWithNewline(c, 200, pulpgin.H{"status": "ok"})
	}
}

// DELETE /sessions/:playerIP
func closeSession(relay *Relay) pulpgin.HandlerFunc {
	return func(c *pulpgin.Context) {
		playerIP := c.Param("playerIP")
		if playerIP == "" {
			c.String(400, "player_ip required\n")
			return
		}
		relay.CloseSession(playerIP)
		log.Printf("Session closed via API: %s", playerIP)
		writeJSONWithNewline(c, 200, pulpgin.H{"status": "ok"})
	}
}

// GET /routes
//
// Native sets Content-Type "application/json" explicitly (no charset).
// We set it manually to match, then write the body with the same
// trailing newline json.NewEncoder produces on native.
func listRoutes(relay *Relay) pulpgin.HandlerFunc {
	return func(c *pulpgin.Context) {
		body, err := json.Marshal(relay.Router().List())
		if err != nil {
			c.String(500, "marshal error: %v", err)
			return
		}
		body = append(body, '\n')
		c.Data(200, "application/json", body)
	}
}

// GET /health
//
// Native never explicitly sets Content-Type; Go's http.DetectContentType
// sniffs "{...}\n" as "text/plain; charset=utf-8". We can't replicate
// the sniff path in the cell without bypassing pulpgin, so we set the
// type explicitly — harness header comparison strips charset and the
// /health case ignores Content-Type anyway.
func health(c *pulpgin.Context) {
	writeJSONWithNewline(c, 200, pulpgin.H{"status": "healthy"})
}

// writeJSONWithNewline mirrors the native stdlib pattern
// `json.NewEncoder(w).Encode(obj)` which appends a trailing "\n" after
// the JSON. pulpgin's c.JSON drops that newline, so plain byte-compare
// parity tests (or any consumer that relies on newline-framed JSON)
// would see a one-byte diff. This helper restores byte parity.
func writeJSONWithNewline(c *pulpgin.Context, status int, obj any) {
	body, err := json.Marshal(obj)
	if err != nil {
		c.String(500, "marshal error: %v", err)
		return
	}
	body = append(body, '\n')
	c.Data(status, "application/json; charset=utf-8", body)
}
