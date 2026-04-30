package main

// Router maps player IPs to backend addresses.
//
// Single-threaded in WASM — no sync.RWMutex needed. The step loop is
// serial, so every Get/Set/Delete/List call happens from the same
// goroutine that owns the map.
type Router struct {
	routes map[string]string
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return &Router{
		routes: make(map[string]string),
	}
}

// Set maps a player IP to a backend.
func (r *Router) Set(playerIP, backend string) {
	r.routes[playerIP] = backend
}

// Get returns the backend for a player IP.
func (r *Router) Get(playerIP string) (string, bool) {
	backend, ok := r.routes[playerIP]
	return backend, ok
}

// Delete removes a player's route.
func (r *Router) Delete(playerIP string) {
	delete(r.routes, playerIP)
}

// List returns a copy of all current routes (for debugging).
func (r *Router) List() map[string]string {
	out := make(map[string]string, len(r.routes))
	for k, v := range r.routes {
		out[k] = v
	}
	return out
}
