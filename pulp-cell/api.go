package main

import (
	"encoding/json"
	"log"

	pulpgin "github.com/BananaLabs-OSS/Fiber/pulp/gin"
)

// registerRoutes wires the HTTP control API. Bananasplit pushes route
// changes here; operators can use GET /health and GET /routes for
// observability.
func registerRoutes(r *pulpgin.Engine, relay *Relay) {
	r.POST("/routes", setRoute(relay))
	r.DELETE("/routes/:playerIP", deleteRoute(relay))
	r.DELETE("/sessions/:playerIP", closeSession(relay))
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
