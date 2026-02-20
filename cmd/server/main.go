package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bananalabs-oss/peel/internal/relay"
	"github.com/bananalabs-oss/potassium/config"
)

func main() {
	// CLI flags
	listenAddr := flag.String("listen", "", "UDP relay address (default :5520)")
	apiAddr := flag.String("api", "", "HTTP API address (default :8080)")
	bananasplitURL := flag.String("bananasplit", "", "Bananasplit URL (default http://localhost:3000)")
	bufferSize := flag.Int("buffer", 0, "Socket buffer size in bytes (default 8388608)")
	flag.Parse()

	// Resolve: CLI > Env > Default
	config := struct {
		ListenAddr     string
		APIAddr        string
		BananasplitURL string
		BufferSize     int
	}{
		ListenAddr:     config.Resolve(*listenAddr, config.EnvOrDefault("PEEL_LISTEN_ADDR", ""), ":5520"),
		APIAddr:        config.Resolve(*apiAddr, config.EnvOrDefault("PEEL_API_ADDR", ""), ":8080"),
		BananasplitURL: config.Resolve(*bananasplitURL, config.EnvOrDefault("BANANASPLIT_URL", ""), "http://localhost:3001"),
		BufferSize:     config.ResolveInt(*bufferSize, config.EnvOrDefaultInt("PEEL_BUFFER_SIZE", 0), 8*1024*1024),
	}

	// Create relay
	r := relay.New(config.ListenAddr, "", config.BananasplitURL, config.BufferSize)

	// Start API
	api := relay.NewAPI(r, config.APIAddr)
	go api.Start()

	// Start relay
	go r.Start()

	log.Printf("Peel relay listening on %s", config.ListenAddr)
	log.Printf("API listening on %s", config.APIAddr)
	log.Printf("Bananasplit URL: %s", config.BananasplitURL)
	log.Printf("Buffer size: %d bytes", config.BufferSize)

	// Wait for shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	r.Stop()
}

