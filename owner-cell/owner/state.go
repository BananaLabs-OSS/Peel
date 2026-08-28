package owner

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

type persistedState struct {
	Routes        map[string]string  `msgpack:"routes"`
	Sessions      map[string]Session `msgpack:"sessions"`
	NegativeCache map[string]string  `msgpack:"negative_cache"`
	Applied       map[string][]byte  `msgpack:"applied"`
}

type State struct {
	mu sync.RWMutex
	db *sql.DB
	persistedState
}

func emptyState() persistedState {
	return persistedState{
		Routes: map[string]string{}, Sessions: map[string]Session{},
		NegativeCache: map[string]string{}, Applied: map[string][]byte{},
	}
}

func New() *State { return &State{persistedState: emptyState()} }

func Open(db *sql.DB) (*State, error) {
	if db == nil {
		return nil, fmt.Errorf("routing state database is required")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS peel_owner_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		payload BLOB NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("migrate routing state: %w", err)
	}
	state := &State{db: db, persistedState: emptyState()}
	var payload []byte
	err := db.QueryRow(`SELECT payload FROM peel_owner_state WHERE id = 1`).Scan(&payload)
	if err == nil {
		if err := msgpack.Unmarshal(payload, &state.persistedState); err != nil {
			return nil, fmt.Errorf("decode routing state state: %w", err)
		}
		state.normalize()
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("load routing state state: %w", err)
	}
	return state, nil
}

func (s *State) normalize() {
	if s.Routes == nil {
		s.Routes = map[string]string{}
	}
	if s.Sessions == nil {
		s.Sessions = map[string]Session{}
	}
	if s.NegativeCache == nil {
		s.NegativeCache = map[string]string{}
	}
	if s.Applied == nil {
		s.Applied = map[string][]byte{}
	}
}

func (s *State) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func clone(value persistedState) (persistedState, error) {
	raw, err := msgpack.Marshal(value)
	if err != nil {
		return persistedState{}, err
	}
	var copied persistedState
	if err := msgpack.Unmarshal(raw, &copied); err != nil {
		return persistedState{}, err
	}
	return copied, nil
}

func (s *State) apply(id string, fn func() (any, error)) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("command id is required")
	}
	if replay, ok := s.Applied[id]; ok {
		return append([]byte(nil), replay...), nil
	}
	before, err := clone(s.persistedState)
	if err != nil {
		return nil, err
	}
	value, err := fn()
	if err != nil {
		s.persistedState = before
		return nil, err
	}
	encoded, err := msgpack.Marshal(value)
	if err != nil {
		s.persistedState = before
		return nil, err
	}
	s.Applied[id] = append([]byte(nil), encoded...)
	if err := s.persistLocked(); err != nil {
		s.persistedState = before
		return nil, err
	}
	return encoded, nil
}

func (s *State) persistLocked() error {
	if s.db == nil {
		return nil
	}
	payload, err := msgpack.Marshal(s.persistedState)
	if err != nil {
		return fmt.Errorf("encode routing state state: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO peel_owner_state(id,payload) VALUES(1,?)
		ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, payload); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("persist routing state state: %w", err)
	}
	return tx.Commit()
}

func parseUint(value, field string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned decimal string", field)
	}
	return parsed, nil
}

func (s *State) RouteSet(req RouteSetRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		req.Key, req.Target = strings.TrimSpace(req.Key), strings.TrimSpace(req.Target)
		if req.Key == "" || req.Target == "" {
			return nil, fmt.Errorf("key and target are required")
		}
		old, had := s.Routes[req.Key]
		s.Routes[req.Key] = req.Target
		return RouteSetResult{
			Changed: old != req.Target, HadRoute: had, OldTarget: old,
			Route: Route{Key: req.Key, Target: req.Target},
		}, nil
	})
}

func (s *State) RouteGet(req KeyQuery) RouteGetResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.Routes[req.Key]
	if !ok {
		return RouteGetResult{}
	}
	route := Route{Key: req.Key, Target: target}
	return RouteGetResult{Found: true, Route: &route}
}

func (s *State) RouteDelete(req RouteDeleteRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		target, ok := s.Routes[req.Key]
		delete(s.Routes, req.Key)
		if !ok {
			return RouteDeleteResult{}, nil
		}
		route := Route{Key: req.Key, Target: target}
		return RouteDeleteResult{Deleted: true, Route: &route}, nil
	})
}

func (s *State) RouteList() RouteListResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	routes := make(map[string]string, len(s.Routes))
	for groupKey, target := range s.Routes {
		routes[groupKey] = target
	}
	return RouteListResult{Routes: routes}
}

func (s *State) SessionTouch(req SessionTouchRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		if req.Key == "" || req.SourceEndpoint == "" || req.Target == "" {
			return nil, fmt.Errorf("key, source_endpoint, and target are required")
		}
		if req.SessionKey == "" {
			req.SessionKey = req.SourceEndpoint
		}
		if _, err := parseUint(req.LastActivity, "last_activity"); err != nil {
			return nil, err
		}
		session := req.Session
		session.SessionKey = req.SessionKey
		s.Sessions[req.SessionKey] = session
		return SessionResult{Found: true, Session: &session}, nil
	})
}

func (s *State) SessionClose(req SessionCloseRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		if req.SessionKey != "" {
			session, ok := s.Sessions[req.SessionKey]
			delete(s.Sessions, req.SessionKey)
			if !ok {
				return SessionResult{}, nil
			}
			return SessionResult{Found: true, Session: &session}, nil
		}
		var closed *Session
		for key, session := range s.Sessions {
			if session.Key == req.Key {
				copy := session
				closed = &copy
				delete(s.Sessions, key)
			}
		}
		return SessionResult{Found: closed != nil, Session: closed}, nil
	})
}

func (s *State) SessionSweep(req SessionSweepRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		wall, err := parseUint(req.Clock, "clock")
		if err != nil {
			return nil, err
		}
		idle, err := parseUint(req.IdleFor, "idle_for")
		if err != nil {
			return nil, err
		}
		closed := []string{}
		if idle == 0 {
			return SessionSweepResult{Closed: closed}, nil
		}
		for sessionKey, session := range s.Sessions {
			last, err := parseUint(session.LastActivity, "stored last_activity")
			if err != nil {
				return nil, err
			}
			if wall > last && wall-last > idle {
				delete(s.Sessions, sessionKey)
				closed = append(closed, sessionKey)
				if !hasSessionForGroup(s.Sessions, session.Key) {
					delete(s.Routes, session.Key)
				}
			}
		}
		sort.Strings(closed)
		return SessionSweepResult{Closed: closed}, nil
	})
}

func hasSessionForGroup(sessions map[string]Session, groupKey string) bool {
	for _, session := range sessions {
		if session.Key == groupKey {
			return true
		}
	}
	return false
}

func (s *State) NegativeRecord(req NegativeRecordRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		if req.Key == "" {
			return nil, fmt.Errorf("key is required")
		}
		if _, err := parseUint(req.ExpiresAt, "expires_at"); err != nil {
			return nil, err
		}
		s.NegativeCache[req.Key] = req.ExpiresAt
		return NegativeCheckResult{Blocked: true}, nil
	})
}

func (s *State) NegativeCheck(req NegativeCheckRequest) (NegativeCheckResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now, err := parseUint(req.Now, "now")
	if err != nil {
		return NegativeCheckResult{}, err
	}
	expiry, ok := s.NegativeCache[req.Key]
	if !ok {
		return NegativeCheckResult{}, nil
	}
	exp, err := parseUint(expiry, "stored expiry")
	if err != nil {
		return NegativeCheckResult{}, err
	}
	return NegativeCheckResult{Blocked: now < exp}, nil
}

func (s *State) NegativeClear(req NegativeClearRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		delete(s.NegativeCache, req.Key)
		return NegativeCheckResult{}, nil
	})
}

func (s *State) NegativeSweep(req NegativeSweepRequest) ([]byte, error) {
	return s.apply(req.ID, func() (any, error) {
		now, err := parseUint(req.Now, "now")
		if err != nil {
			return nil, err
		}
		removed := 0
		for groupKey, expiry := range s.NegativeCache {
			exp, err := parseUint(expiry, "stored expiry")
			if err != nil {
				return nil, err
			}
			if now >= exp {
				delete(s.NegativeCache, groupKey)
				removed++
			}
		}
		return NegativeSweepResult{Removed: removed}, nil
	})
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied, _ := clone(s.persistedState)
	return Snapshot{
		Routes: copied.Routes, Sessions: copied.Sessions,
		NegativeCache: copied.NegativeCache,
	}
}
