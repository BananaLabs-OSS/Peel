package main

import (
	"fmt"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
)

const stateCell = "routing-state"

type StateClient struct{}

type stateSweepResult struct {
	Closed []string `msgpack:"closed"`
}

type stateSnapshot struct {
	Routes   map[string]string `msgpack:"routes"`
	Sessions map[string]struct {
		Key    string `msgpack:"key"`
		Target string `msgpack:"target"`
	} `msgpack:"sessions"`
}

func stateCall[T any](provider string, request any) (T, error) {
	var result T
	payload, err := msgpack.Marshal(request)
	if err != nil {
		return result, err
	}
	response, err := pulp.Call(stateCell, provider, payload)
	if err != nil {
		return result, fmt.Errorf("%s: %w", provider, err)
	}
	if err := msgpack.Unmarshal(response, &result); err != nil {
		return result, fmt.Errorf("decode %s: %w", provider, err)
	}
	return result, nil
}

func (StateClient) SessionTouch(id, sessionKey, routeKey, sourceEndpoint, target, lastActivity string) error {
	_, err := stateCall[map[string]any]("routing.state.v1.session.touch", map[string]any{
		"id": id, "session_key": sessionKey, "key": routeKey, "source_endpoint": sourceEndpoint,
		"target": target, "last_activity": lastActivity,
	})
	return err
}

func (StateClient) Sweep(id, wall, idle string) ([]string, error) {
	result, err := stateCall[stateSweepResult]("routing.state.v1.session.sweep", map[string]any{
		"id": id, "clock": wall, "idle_for": idle,
	})
	return result.Closed, err
}

func (StateClient) Snapshot() (stateSnapshot, error) {
	return stateCall[stateSnapshot]("routing.state.v1.snapshot.get", map[string]any{})
}
