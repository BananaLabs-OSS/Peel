package composition_test

import (
	"os"
	"testing"

	"github.com/BananaLabs-OSS/Fiber/pulp/workflow"
	"github.com/BananaLabs-OSS/Pulp-Lua/orchestrator"
	"github.com/vmihailenco/msgpack/v5"
)

type recordedCall struct {
	cell, provider string
	payload        map[string]any
}

type fakeCaller struct{ calls []recordedCall }

func (f *fakeCaller) Call(cell, provider string, raw []byte) ([]byte, error) {
	var payload map[string]any
	if err := msgpack.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	f.calls = append(f.calls, recordedCall{cell: cell, provider: provider, payload: payload})
	switch provider {
	case "routing.state.v1.route.get":
		return msgpack.Marshal(map[string]any{"found": true, "route": map[string]any{"key": payload["key"], "target": "game-a:5520"}})
	case "routing.state.v1.route.set":
		return msgpack.Marshal(map[string]any{"changed": true, "had_route": false})
	default:
		return msgpack.Marshal(map[string]any{"ok": true})
	}
}

func loadRuntime(t *testing.T, caller *fakeCaller) *orchestrator.Runtime {
	t.Helper()
	script, err := os.ReadFile("peel.lua")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := orchestrator.New(orchestrator.Options{Script: string(script), Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func TestPeelPolicyMapsNATEndpointToGenericRoute(t *testing.T) {
	caller := &fakeCaller{}
	runtime := loadRuntime(t, caller)
	result, err := runtime.Dispatch(workflow.DispatchRequest{Event: "peel.route.resolve.v1", Payload: map[string]any{
		"source_endpoint": "203.0.113.7:45000", "session_key": "udp:203.0.113.7:45000", "now_millis": int64(1000),
	}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := workflow.DecodeValue[map[string]any](result)
	if err != nil {
		t.Fatal(err)
	}
	if value["key"] != "203.0.113.7" || value["target"] != "game-a:5520" {
		t.Fatalf("unexpected route: %#v", value)
	}
	if len(caller.calls) != 1 || caller.calls[0].cell != "routing-state" || caller.calls[0].payload["key"] != "203.0.113.7" {
		t.Fatalf("Peel policy did not translate into generic state call: %#v", caller.calls)
	}
}
