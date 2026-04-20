// Package wire owns the WebSocket lifecycle: reading, writing, heartbeat
// replies, and routing decoded envelopes to per-session handlers.
//
// It is intentionally separate from internal/protocol (which is a pure
// codec) so that the framing is testable without goroutines and the pump
// is testable without network plumbing.
package wire

import (
	"maps"
	"sync"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// Handler processes one envelope addressed to a session.
type Handler func(env protocol.Envelope)

// Registry is a thread-safe map from session id to handler.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{handlers: map[string]Handler{}} }

// Register installs a handler for a session id. Replaces any prior handler
// for the same id.
func (r *Registry) Register(id string, h Handler) {
	r.mu.Lock()
	r.handlers[id] = h
	r.mu.Unlock()
}

// Unregister removes a handler.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	delete(r.handlers, id)
	r.mu.Unlock()
}

// Lookup returns the handler for id, if any.
func (r *Registry) Lookup(id string) (Handler, bool) {
	r.mu.RLock()
	h, ok := r.handlers[id]
	r.mu.RUnlock()
	return h, ok
}

// Snapshot returns a copy of the current id→handler map. Used by Client.Close
// to close every session's updates channel.
func (r *Registry) Snapshot() map[string]Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Handler, len(r.handlers))
	maps.Copy(out, r.handlers)
	return out
}
