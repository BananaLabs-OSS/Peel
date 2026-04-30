package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// appConfig is the msgpack-decoded [config] table from pulp.cell.toml.
type appConfig struct {
	ListenAddr     string
	APIAddr        string
	BananasplitURL string
	BufferSize     int
	IdleTimeout    time.Duration
}

func parseConfig(data []byte) (appConfig, error) {
	var cfg appConfig
	if len(data) == 0 {
		return cfg, fmt.Errorf("missing [config]")
	}

	var raw map[string]any
	if err := decodeMsgpack(data, &raw); err != nil {
		return cfg, err
	}
	jbytes, _ := json.Marshal(raw)

	var tmp struct {
		ListenAddr     string `json:"listen_addr"`
		APIAddr        string `json:"api_addr"`
		BananasplitURL string `json:"bananasplit_url"`
		BufferSize     int    `json:"buffer_size"`
		IdleTimeout    string `json:"idle_timeout"`
	}
	if err := json.Unmarshal(jbytes, &tmp); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}

	cfg.ListenAddr = tmp.ListenAddr
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":5520"
	}
	cfg.APIAddr = tmp.APIAddr
	// HTTP_PORT env (set by Pulp host's -http-port flag) wins over
	// the manifest default so the parity harness can pick ephemeral
	// ports and single-operator deployments can point the shared
	// listener at one address.
	if hp := os.Getenv("HTTP_PORT"); hp != "" {
		cfg.APIAddr = ":" + hp
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = ":8080"
	}
	cfg.BananasplitURL = tmp.BananasplitURL
	if cfg.BananasplitURL == "" {
		cfg.BananasplitURL = "http://localhost:3001"
	}
	cfg.BufferSize = tmp.BufferSize
	if cfg.BufferSize == 0 {
		cfg.BufferSize = 8 * 1024 * 1024
	}

	idle := tmp.IdleTimeout
	if idle == "" {
		idle = "10m"
	}
	d, err := time.ParseDuration(idle)
	if err != nil {
		return cfg, fmt.Errorf("invalid idle_timeout %q: %w", idle, err)
	}
	cfg.IdleTimeout = d

	return cfg, nil
}
