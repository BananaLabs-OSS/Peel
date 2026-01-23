package relay

import "sync"

// Router maps player IPs to backend addresses
type Router struct {
	routes sync.Map // playerIP → backendAddr
}

// NewRouter creates a new router
func NewRouter() *Router {
	return &Router{}
}

// Set maps a player IP to a backend
func (r *Router) Set(playerIP, backend string) {
	r.routes.Store(playerIP, backend)
}

// Get returns the backend for a player IP
func (r *Router) Get(playerIP string) (string, bool) {
	val, ok := r.routes.Load(playerIP)
	if !ok {
		return "", false
	}
	return val.(string), true
}

// Delete removes a player's route
func (r *Router) Delete(playerIP string) {
	r.routes.Delete(playerIP)
}

// List returns all current routes (for debugging)
func (r *Router) List() map[string]string {
	result := make(map[string]string)
	r.routes.Range(func(key, value any) bool {
		result[key.(string)] = value.(string)
		return true
	})
	return result
}
