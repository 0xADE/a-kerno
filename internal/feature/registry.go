// Package feature provides the ADE feature registry.
// Each daemon managed by a-kerno is registered as a feature.
// The registry exports the ADE_FEATURES environment variable
// consumed by adm and other ADE components.
package feature

import (
	"sort"
	"strings"
	"sync"
)

// Feature represents a single ADE feature provided by a daemon.
type Feature struct {
	Name    string
	Version string
	Socket  string
	Ready   bool
}

// Registry is a thread-safe registry of ADE features.
type Registry struct {
	features map[string]*Feature
	mu       sync.RWMutex
}

// NewRegistry creates a new empty feature registry.
func NewRegistry() *Registry {
	return &Registry{
		features: make(map[string]*Feature),
	}
}

// Register adds a new feature to the registry. If a feature with the same
// name already exists, it is overwritten.
func (r *Registry) Register(name, version, socket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[name] = &Feature{
		Name:    name,
		Version: version,
		Socket:  socket,
		Ready:   false,
	}
}

// SetReady sets the ready status of a feature. If the feature does not
// exist in the registry, the call is a no-op.
func (r *Registry) SetReady(name string, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.features[name]; ok {
		f.Ready = ready
	}
}

// Get returns a feature by name, or nil if not found.
func (r *Registry) Get(name string) *Feature {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.features[name]
}

// List returns a slice of all registered features, sorted alphabetically
// by name. The returned slice is a copy — callers are free to modify it.
func (r *Registry) List() []Feature {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Feature, 0, len(r.features))
	for _, f := range r.features {
		result = append(result, *f)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ExportEnv returns the ADE_FEATURES environment variable value.
// Format: name1:version1:ready1,name2:version2:ready2
// where ready is "1" (true) or "0" (false).
func (r *Registry) ExportEnv() string {
	features := r.List()
	parts := make([]string, 0, len(features))
	for _, f := range features {
		readyStr := "0"
		if f.Ready {
			readyStr = "1"
		}
		parts = append(parts, f.Name+":"+f.Version+":"+readyStr)
	}
	return strings.Join(parts, ",")
}
