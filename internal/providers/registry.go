// internal/providers/registry.go
// Purpose: Provider registry and API key wiring.
package providers

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds all registered providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]AIProvider
	keys      *KeyStore
	client    *HTTPClient
}

// NewRegistry creates a provider registry.
func NewRegistry(keys *KeyStore, client *HTTPClient) *Registry {
	return &Registry{
		providers: make(map[string]AIProvider),
		keys:      keys,
		client:    client,
	}
}

// Register adds a provider and hydrates its API key from the keystore.
func (r *Registry) Register(p AIProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[p.Name()] = p

	if key := r.keys.Get(p.Name()); key != "" {
		p.SetAPIKey(key)
	}
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (AIProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", name)
	}

	return p, nil
}

// Names returns sorted provider names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

// SetAPIKey stores a key and applies it to the provider.
func (r *Registry) SetAPIKey(provider, key string) error {
	p, err := r.Get(provider)
	if err != nil {
		return err
	}

	p.SetAPIKey(key)
	return r.keys.Set(provider, key)
}

// HasAPIKey reports whether a provider key is available.
func (r *Registry) HasAPIKey(provider string) bool {
	return r.keys.Has(provider)
}

// APIKey returns the stored key for a provider, or "".
//
// Exists so the speech wizard can adopt a key the user already entered for the
// same vendor on the AI side. The two keystores are separate files by design
// (speech can run without an LLM), but a person who typed their OpenAI key once
// should not be asked for it again three screens later.
func (r *Registry) APIKey(provider string) string {
	return r.keys.Get(provider)
}

// Client returns the shared HTTP client.
func (r *Registry) Client() *HTTPClient {
	return r.client
}

// Keys returns the keystore.
func (r *Registry) Keys() *KeyStore {
	return r.keys
}
