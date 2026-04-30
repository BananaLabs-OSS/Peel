// Peel — Pulp cell port.
//
// UDP relay for Minecraft traffic. Listens on a configured UDP port,
// looks up per-player routes (either cached or fetched from Bananasplit),
// and forwards packets to the backend container. Each active player
// gets a dedicated outbound UDP socket so backend replies return on
// the right flow. Also exposes an HTTP control API that Bananasplit
// uses to push or revoke routes.
//
// Originally a standalone Go service: cmd/server/main.go, internal/relay/.
//
// Build:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o peel.wasm .
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	pulpgin "github.com/BananaLabs-OSS/Fiber/pulp/gin"
	"github.com/BananaLabs-OSS/Fiber/pulp/udp"
)

func main() {}

func init() {
	pulp.OnInit(bootstrap)
}

func bootstrap(configBytes []byte) error {
	cfg, err := parseConfig(configBytes)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// --- Relay ---
	relay := New(cfg.ListenAddr, cfg.BananasplitURL, cfg.BufferSize, cfg.IdleTimeout)
	if err := relay.Start(); err != nil {
		return fmt.Errorf("relay start: %w", err)
	}

	// --- HTTP control API ---
	//
	// Bind an alt listener at cfg.APIAddr only if it differs from the
	// host's default HTTP_PORT. When they match (e.g. the parity
	// harness forwards ${PORT} into both), the routes ride the default
	// listener and a second bind would fail with EADDRINUSE. Native
	// Peel runs with distinct API + UDP ports; WASM keeps that model
	// on production deployments and collapses to single-port in tests.
	defaultPort := os.Getenv("HTTP_PORT")
	if defaultPort == "" || cfg.APIAddr != ":"+defaultPort {
		if err := pulp.HTTP.Listen(cfg.APIAddr); err != nil {
			return fmt.Errorf("http listen %s: %w", cfg.APIAddr, err)
		}
	}
	r := pulpgin.New()
	registerRoutes(r, relay)
	if err := r.RegisterRoutes(); err != nil {
		return fmt.Errorf("register routes: %w", err)
	}

	// --- Compose step handler ---
	//
	// Order matters: udp.Dispatch must run before the gin engine
	// dispatches, because Engine.Dispatch returns nil (not an error)
	// for non-HTTP/WS events but the Pulp-ext-udp events would
	// otherwise silently be ignored. Running UDP first lets both
	// subsystems see their own event kinds and ignore everything else.
	pulp.OnStep(func(ev pulp.StepEvent) error {
		if err := udp.Dispatch(ev); err != nil {
			return err
		}
		relay.SweepIdle(ev.WallTime)
		return r.Dispatch(ev)
	})

	pulp.OnShutdown(func() error {
		log.Println("Shutting down...")
		relay.Stop()
		return nil
	})

	// Startup banner — mirrors native cmd/server/main.go's four log
	// lines verbatim so any log-scraping parity check sees identical
	// output. Idle timeout is cell-only state (native has no equivalent)
	// so it's emitted as a trailing debug line that native won't have;
	// harness never compares stderr logs but leaving it labeled keeps
	// grep-based forensic diffs obvious.
	log.Printf("Peel relay listening on %s", cfg.ListenAddr)
	log.Printf("API listening on %s", cfg.APIAddr)
	log.Printf("Bananasplit URL: %s", cfg.BananasplitURL)
	log.Printf("Buffer size: %d bytes", cfg.BufferSize)
	return nil
}
