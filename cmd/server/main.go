package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bananalabs-oss/peel/internal/relay"
)

func main() {
	// Config from env
	listenAddr := getEnv("PEEL_LISTEN_ADDR", ":5520")
	apiAddr := getEnv("PEEL_API_ADDR", ":8080")
	defaultBackend := getEnv("PEEL_DEFAULT_BACKEND", "")
	bananasplitURL := getEnv("BANANASPLIT_URL", "http://localhost:3001")

	// Create relay
	r := relay.New(listenAddr, defaultBackend, bananasplitURL)

	// Start API
	api := relay.NewAPI(r, apiAddr)
	go api.Start()

	// Start relay
	go r.Start()

	log.Printf("Peel relay listening on %s", listenAddr)
	log.Printf("API listening on %s", apiAddr)

	// Wait for shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	r.Stop()
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
