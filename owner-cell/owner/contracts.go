package owner

const (
	FnRouteSet       = "routing.state.v1.route.set"
	FnRouteGet       = "routing.state.v1.route.get"
	FnRouteDelete    = "routing.state.v1.route.delete"
	FnRouteList      = "routing.state.v1.route.list"
	FnSessionTouch   = "routing.state.v1.session.touch"
	FnSessionClose   = "routing.state.v1.session.close"
	FnSessionSweep   = "routing.state.v1.session.sweep"
	FnNegativeRecord = "routing.state.v1.negative.record"
	FnNegativeCheck  = "routing.state.v1.negative.check"
	FnNegativeClear  = "routing.state.v1.negative.clear"
	FnNegativeSweep  = "routing.state.v1.negative.sweep"
	FnSnapshot       = "routing.state.v1.snapshot.get"
)

type Command struct {
	ID string `msgpack:"id"`
}

type Route struct {
	Key    string `msgpack:"key"`
	Target string `msgpack:"target"`
}

type RouteSetRequest struct {
	Command
	Route
}

type RouteSetResult struct {
	Changed   bool   `msgpack:"changed"`
	HadRoute  bool   `msgpack:"had_route"`
	OldTarget string `msgpack:"old_target,omitempty"`
	Route     Route  `msgpack:"route"`
}

type KeyQuery struct {
	Key string `msgpack:"key"`
}

type RouteGetResult struct {
	Found bool   `msgpack:"found"`
	Route *Route `msgpack:"route,omitempty"`
}

type RouteDeleteRequest struct {
	Command
	Key string `msgpack:"key"`
}

type RouteDeleteResult struct {
	Deleted bool   `msgpack:"deleted"`
	Route   *Route `msgpack:"route,omitempty"`
}

type RouteListResult struct {
	Routes map[string]string `msgpack:"routes"`
}

type Session struct {
	SessionKey     string `msgpack:"session_key"`
	Key            string `msgpack:"key"`
	SourceEndpoint string `msgpack:"source_endpoint"`
	Target         string `msgpack:"target"`
	LastActivity   string `msgpack:"last_activity"`
}

type SessionTouchRequest struct {
	Command
	Session
}

type SessionResult struct {
	Found   bool     `msgpack:"found"`
	Session *Session `msgpack:"session,omitempty"`
}

type SessionCloseRequest struct {
	Command
	Key        string `msgpack:"key"`
	SessionKey string `msgpack:"session_key,omitempty"`
}

type SessionSweepRequest struct {
	Command
	Clock   string `msgpack:"clock"`
	IdleFor string `msgpack:"idle_for"`
}

type SessionSweepResult struct {
	Closed []string `msgpack:"closed"`
}

type NegativeRecordRequest struct {
	Command
	Key       string `msgpack:"key"`
	ExpiresAt string `msgpack:"expires_at"`
}

type NegativeCheckRequest struct {
	Key string `msgpack:"key"`
	Now string `msgpack:"now"`
}

type NegativeCheckResult struct {
	Blocked bool `msgpack:"blocked"`
}

type NegativeClearRequest struct {
	Command
	Key string `msgpack:"key"`
}

type NegativeSweepRequest struct {
	Command
	Now string `msgpack:"now"`
}

type NegativeSweepResult struct {
	Removed int `msgpack:"removed"`
}

type Snapshot struct {
	Routes        map[string]string  `msgpack:"routes"`
	Sessions      map[string]Session `msgpack:"sessions"`
	NegativeCache map[string]string  `msgpack:"negative_cache"`
}
