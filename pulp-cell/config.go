package main

import (
	"fmt"
	"time"

	"github.com/BananaLabs-OSS/Fiber/pulp/cellconfig"
)

// appConfig is the msgpack-decoded [config] table from pulp.cell.toml.
type appConfig struct {
	ListenAddr       string
	BufferSize       int
	IdleTimeout      time.Duration
	RouteEvent       string
	RouteResolverURL string
}

func parseConfig(data []byte) (appConfig, error) {
	var cfg appConfig
	if len(data) == 0 {
		return cfg, fmt.Errorf("missing [config]")
	}

	var tmp struct {
		ListenAddr       string `json:"listen_addr"`
		BufferSize       int    `json:"buffer_size"`
		IdleTimeout      string `json:"idle_timeout"`
		RouteEvent       string `json:"route_event"`
		RouteResolverURL string `json:"route_resolver_url"`
	}
	if err := cellconfig.Decode(data, &tmp); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}

	cfg.ListenAddr = tmp.ListenAddr
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":5520"
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

	cfg.RouteEvent = tmp.RouteEvent
	if cfg.RouteEvent == "" {
		cfg.RouteEvent = "route.resolve.v1"
	}
	cfg.RouteResolverURL = tmp.RouteResolverURL

	return cfg, nil
}
