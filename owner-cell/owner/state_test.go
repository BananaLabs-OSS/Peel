package owner

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
	_ "modernc.org/sqlite"
)

func openTestState(t *testing.T, path string) *State {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := Open(db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return state
}

func decodeResult[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := msgpack.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRestartAndReplayPreserveRoutesSessionsAndNegativeCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peel.db")
	state := openTestState(t, path)
	first, err := state.RouteSet(RouteSetRequest{
		Command: Command{ID: "route-1"}, Route: Route{Key: "203.0.113.1", Target: "game-a:5520"},
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := state.RouteSet(RouteSetRequest{
		Command: Command{ID: "route-1"}, Route: Route{Key: "wrong", Target: "wrong:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(replay) {
		t.Fatal("route command did not replay")
	}
	if _, err := state.SessionTouch(SessionTouchRequest{
		Command: Command{ID: "touch-1"},
		Session: Session{Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45000", Target: "game-a:5520", LastActivity: "100"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.NegativeRecord(NegativeRecordRequest{
		Command: Command{ID: "negative-1"}, Key: "203.0.113.2", ExpiresAt: "1000",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := openTestState(t, path)
	t.Cleanup(func() { _ = restarted.Close() })
	snapshot := restarted.Snapshot()
	if snapshot.Routes["203.0.113.1"] != "game-a:5520" {
		t.Fatalf("restart lost route: %#v", snapshot)
	}
	if snapshot.Sessions["203.0.113.1:45000"].LastActivity != "100" {
		t.Fatalf("restart lost session: %#v", snapshot)
	}
	if snapshot.NegativeCache["203.0.113.2"] != "1000" {
		t.Fatalf("restart lost negative cache: %#v", snapshot)
	}
}

func TestSessionsBehindOneNATRemainDistinct(t *testing.T) {
	state := New()
	for _, session := range []Session{
		{SessionKey: "udp:203.0.113.1:45000", Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45000", Target: "game-a:5520", LastActivity: "100"},
		{SessionKey: "udp:203.0.113.1:45001", Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45001", Target: "game-a:5520", LastActivity: "100"},
	} {
		if _, err := state.SessionTouch(SessionTouchRequest{
			Command: Command{ID: "touch:" + session.SessionKey},
			Session: session,
		}); err != nil {
			t.Fatal(err)
		}
	}

	snapshot := state.Snapshot()
	if len(snapshot.Sessions) != 2 {
		t.Fatalf("NAT peers collided: %#v", snapshot.Sessions)
	}
	for _, key := range []string{"udp:203.0.113.1:45000", "udp:203.0.113.1:45001"} {
		if snapshot.Sessions[key].SessionKey != key {
			t.Fatalf("missing session %q: %#v", key, snapshot.Sessions)
		}
	}
}

func TestSweepKeepsSharedRouteUntilLastNATSessionExpires(t *testing.T) {
	state := New()
	if _, err := state.RouteSet(RouteSetRequest{
		Command: Command{ID: "route"}, Route: Route{Key: "203.0.113.1", Target: "game-a:5520"},
	}); err != nil {
		t.Fatal(err)
	}
	for id, session := range map[string]Session{
		"old":   {SessionKey: "udp:203.0.113.1:45000", Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45000", Target: "game-a:5520", LastActivity: "100"},
		"fresh": {SessionKey: "udp:203.0.113.1:45001", Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45001", Target: "game-a:5520", LastActivity: "190"},
	} {
		if _, err := state.SessionTouch(SessionTouchRequest{Command: Command{ID: "touch:" + id}, Session: session}); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := state.SessionSweep(SessionSweepRequest{
		Command: Command{ID: "sweep-one"}, Clock: "201", IdleFor: "50",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := decodeResult[SessionSweepResult](t, raw)
	if len(result.Closed) != 1 || result.Closed[0] != "udp:203.0.113.1:45000" {
		t.Fatalf("unexpected first sweep: %#v", result)
	}
	if state.RouteList().Routes["203.0.113.1"] != "game-a:5520" {
		t.Fatal("shared route was deleted while a NAT peer remained active")
	}

	if _, err := state.SessionSweep(SessionSweepRequest{
		Command: Command{ID: "sweep-two"}, Clock: "300", IdleFor: "50",
	}); err != nil {
		t.Fatal(err)
	}
	if len(state.RouteList().Routes) != 0 {
		t.Fatal("route remained after the last NAT session expired")
	}
}

func TestCloseByKeyClosesEveryNATSession(t *testing.T) {
	state := New()
	for i, addr := range []string{"203.0.113.1:45000", "203.0.113.1:45001"} {
		if _, err := state.SessionTouch(SessionTouchRequest{
			Command: Command{ID: fmt.Sprintf("touch-%d", i)},
			Session: Session{Key: "203.0.113.1", SourceEndpoint: addr, Target: "game-a:5520", LastActivity: "100"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.SessionClose(SessionCloseRequest{
		Command: Command{ID: "close"}, Key: "203.0.113.1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(state.Snapshot().Sessions) != 0 {
		t.Fatalf("player-IP close left NAT sessions behind: %#v", state.Snapshot().Sessions)
	}
}

func TestSweepIsReplaySafeAndClosesRouteWithSession(t *testing.T) {
	state := New()
	if _, err := state.RouteSet(RouteSetRequest{
		Command: Command{ID: "route"}, Route: Route{Key: "203.0.113.1", Target: "game-a:5520"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SessionTouch(SessionTouchRequest{
		Command: Command{ID: "touch"},
		Session: Session{Key: "203.0.113.1", SourceEndpoint: "203.0.113.1:45000", Target: "game-a:5520", LastActivity: "100"},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := state.SessionSweep(SessionSweepRequest{
		Command: Command{ID: "sweep"}, Clock: "201", IdleFor: "100",
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := state.SessionSweep(SessionSweepRequest{
		Command: Command{ID: "sweep"}, Clock: "999", IdleFor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(replay) {
		t.Fatal("sweep did not replay the original closure result")
	}
	result := decodeResult[SessionSweepResult](t, first)
	if len(result.Closed) != 1 || len(state.RouteList().Routes) != 0 {
		t.Fatalf("unexpected sweep result: %#v routes=%#v", result, state.RouteList())
	}
}

func TestNegativeCacheExpiry(t *testing.T) {
	state := New()
	if _, err := state.NegativeRecord(NegativeRecordRequest{
		Command: Command{ID: "record"}, Key: "203.0.113.2", ExpiresAt: "100",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := state.NegativeCheck(NegativeCheckRequest{Key: "203.0.113.2", Now: "99"})
	if err != nil || !before.Blocked {
		t.Fatalf("cache should block before expiry: %#v %v", before, err)
	}
	after, err := state.NegativeCheck(NegativeCheckRequest{Key: "203.0.113.2", Now: "100"})
	if err != nil || after.Blocked {
		t.Fatalf("cache should not block at expiry: %#v %v", after, err)
	}
}

func TestProvidersMatchDeclaredContract(t *testing.T) {
	providers := New().Providers()
	for _, name := range []string{
		FnRouteSet, FnRouteGet, FnRouteDelete, FnRouteList,
		FnSessionTouch, FnSessionClose, FnSessionSweep,
		FnNegativeRecord, FnNegativeCheck, FnNegativeClear, FnNegativeSweep,
		FnSnapshot,
	} {
		if providers[name] == nil {
			t.Fatalf("missing provider %q", name)
		}
	}
	if len(providers) != 12 {
		t.Fatalf("unexpected provider count: %d", len(providers))
	}
}
