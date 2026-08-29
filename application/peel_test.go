package composition_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BananaLabs-OSS/Fiber/pulp/workflow"
	"github.com/BananaLabs-OSS/Pulp-Lua/orchestrator"
	"github.com/vmihailenco/msgpack/v5"
)

func TestPeelConsumesCanonicalSharedEngines(t *testing.T) {
	manifest, err := os.ReadFile("pulp.app.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	for _, expected := range []string{
		"../../pulp-engines/routed-udp-relay-host-cell/pulp.cell.toml",
		"../../pulp-engines/routed-tcp-relay-host-cell/pulp.cell.toml",
		"../../pulp-engines/routing-state-sqlite-cell/pulp.cell.toml",
		"../../pulp-engines/edge-admission-sqlite-cell/pulp.cell.toml",
		"../../pulp-engines/http-json-cell/pulp.cell.toml",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("application manifest does not consume shared engine %q", expected)
		}
	}
	if strings.Contains(text, "framed-edge-demo-host-cell") {
		t.Fatal("production application must not include the framed demo listener")
	}
	demoManifest, err := os.ReadFile("demo.pulp.app.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(demoManifest), "framed-edge-demo-host-cell") {
		t.Fatal("demo application does not include the framed protocol proof")
	}
	for _, retired := range []string{"../owner-cell/", "../http-client-cell/"} {
		if strings.Contains(text, retired) {
			t.Fatalf("application manifest still consumes retired Peel-local engine %q", retired)
		}
	}
	for path, digest := range map[string]string{
		"../../pulp-engines/routed-udp-relay-host-cell/pulp.cell.toml": "1f9fa343995d7b85d590a5c993e37c581a2d4601f3a9b6a3a2635f4e653518e3",
		"../../pulp-engines/routed-tcp-relay-host-cell/pulp.cell.toml": "ac3e2958e0f8b932212c3c3661832e74b967d7e450c146d039a4d64216832c44",
		"../../pulp-engines/routing-state-sqlite-cell/pulp.cell.toml":  "2d6679303b2e9395f1b903326b66668bb2e12bea3ea2e55010fca3cad111b9e5",
		"../../pulp-engines/edge-admission-sqlite-cell/pulp.cell.toml": "08bdabaa7ed1016b098b5e77da77e2ec63abb6f7e8d5c0647053afef1f171405",
		"../../pulp-engines/framed-edge-demo-host-cell/pulp.cell.toml": "4747d61b778cad3dbe117aca269c9e84654d17d6fdd0a92996cef2f208cffa6a",
		"../../pulp-engines/http-json-cell/pulp.cell.toml":             "e36553d92cd88f7bd6a3959c65f274aac2fa8e33c938031641657e3675171546",
	} {
		engineManifest, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(engineManifest), `wasm_sha256 = "`+digest+`"`) {
			t.Fatalf("shared engine %q is not pinned to verified WASM %s", path, digest)
		}
	}

	dockerfile, err := os.ReadFile("../Dockerfile.pulp")
	if err != nil {
		t.Fatal(err)
	}
	containerBuild := string(dockerfile)
	for _, expected := range []string{
		"COPY pulp-engines/ pulp-engines/",
		"WORKDIR /src/pulp-engines/routed-udp-relay-host-cell",
		"WORKDIR /src/pulp-engines/routed-tcp-relay-host-cell",
		"WORKDIR /src/pulp-engines/routing-state-sqlite-cell",
		"WORKDIR /src/pulp-engines/http-json-cell",
		"WORKDIR /src/pulp-engines/edge-admission-sqlite-cell",
		"WORKDIR /src/pulp-engines/framed-edge-demo-host-cell",
		"/pulp-engines/routed-udp-relay-host-cell/routed-udp-relay.wasm",
		"/pulp-engines/routed-tcp-relay-host-cell/routed-tcp-relay.wasm",
		"/pulp-engines/routing-state-sqlite-cell/routing-state.wasm",
		"/pulp-engines/http-json-cell/http-json.wasm",
		"/pulp-engines/edge-admission-sqlite-cell/edge-admission.wasm",
		"/pulp-engines/framed-edge-demo-host-cell/framed-edge-demo.wasm",
	} {
		if !strings.Contains(containerBuild, expected) {
			t.Fatalf("production container does not package shared engine contract %q", expected)
		}
	}
	for _, retired := range []string{
		"WORKDIR /src/Peel/pulp-cell",
		"WORKDIR /src/Peel/owner-cell",
		"WORKDIR /src/Peel/http-client-cell",
	} {
		if strings.Contains(containerBuild, retired) {
			t.Fatalf("production container still builds retired Peel-local engine %q", retired)
		}
	}
}

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
