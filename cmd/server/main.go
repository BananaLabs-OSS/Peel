package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/bananalabs-oss/peel/internal/relay"
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
		ListenAddr:     resolve(*listenAddr, getEnv("PEEL_LISTEN_ADDR", ""), ":5520"),
		APIAddr:        resolve(*apiAddr, getEnv("PEEL_API_ADDR", ""), ":8080"),
		BananasplitURL: resolve(*bananasplitURL, getEnv("BANANASPLIT_URL", ""), "http://localhost:3001"),
		BufferSize:     resolveInt(*bufferSize, getEnvInt("PEEL_BUFFER_SIZE", 0), 8*1024*1024),
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

// resolve returns first non-empty value: cli > env > fallback
func resolve(cli, env, fallback string) string {
	if cli != "" {
		return cli
	}
	if env != "" {
		return env
	}
	return fallback
}

func resolveInt(cli, env, fallback int) int {
	if cli != 0 {
		return cli
	}
	if env != 0 {
		return env
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}
