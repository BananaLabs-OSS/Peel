package owner

import (
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

type Provider func([]byte) ([]byte, error)

func decode(input []byte, value any) error {
	if len(input) == 0 {
		return fmt.Errorf("empty request")
	}
	return msgpack.Unmarshal(input, value)
}

func encode(value any) ([]byte, error) { return msgpack.Marshal(value) }

func handler[T any](fn func(T) ([]byte, error)) Provider {
	return func(input []byte) ([]byte, error) {
		var request T
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return fn(request)
	}
}

func query[T, R any](fn func(T) R) Provider {
	return func(input []byte) ([]byte, error) {
		var request T
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return encode(fn(request))
	}
}

func queryErr[T, R any](fn func(T) (R, error)) Provider {
	return func(input []byte) ([]byte, error) {
		var request T
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		value, err := fn(request)
		if err != nil {
			return nil, err
		}
		return encode(value)
	}
}

func (s *State) Providers() map[string]Provider {
	return map[string]Provider{
		FnRouteSet: handler(s.RouteSet), FnRouteGet: query(s.RouteGet),
		FnRouteDelete:  handler(s.RouteDelete),
		FnRouteList:    func([]byte) ([]byte, error) { return encode(s.RouteList()) },
		FnSessionTouch: handler(s.SessionTouch), FnSessionClose: handler(s.SessionClose),
		FnSessionSweep:   handler(s.SessionSweep),
		FnNegativeRecord: handler(s.NegativeRecord), FnNegativeCheck: queryErr(s.NegativeCheck),
		FnNegativeClear: handler(s.NegativeClear), FnNegativeSweep: handler(s.NegativeSweep),
		FnSnapshot: func([]byte) ([]byte, error) { return encode(s.Snapshot()) },
	}
}
