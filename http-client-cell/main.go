package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {}

func init() {
	pulp.OnInit(func([]byte) error {
		pulp.Provide("engine.http-json.v1.request", requestJSON)
		return nil
	})
}

type request struct {
	Method    string            `msgpack:"method"`
	URL       string            `msgpack:"url"`
	Headers   map[string]string `msgpack:"headers"`
	Body      any               `msgpack:"body"`
	TimeoutMS int64             `msgpack:"timeout_ms"`
}

func requestJSON(input []byte) ([]byte, error) {
	var req request
	if err := msgpack.Unmarshal(input, &req); err != nil {
		return nil, err
	}
	if req.Method == "" || req.URL == "" {
		return nil, fmt.Errorf("method and url are required")
	}
	body, err := json.Marshal(req.Body)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	resp, err := pulp.HTTP.Fetch(pulp.HTTPFetchRequest{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: body, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	var value any
	if len(resp.Body) > 0 && json.Unmarshal(resp.Body, &value) != nil {
		value = string(resp.Body)
	}
	return msgpack.Marshal(map[string]any{"status": resp.Status, "value": value})
}
