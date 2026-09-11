// internal/providers/modelcache.go
// Purpose: remember what each provider said its models were, so Helix can pick
// and validate a model without a network call.
//
// WHY A CACHE AND NOT A LIVE CALL. DefaultModel() is consulted per turn and
// inside loops over every registered provider (ProviderVisionCapable,
// VisionCapableProviders). Putting a /models request behind any of those puts
// network latency in the hot path — the mistake brain_health.go's header
// comment exists to prevent — and would make a shell with no connectivity
// unable to answer "which model would you use".
//
// WHY IT COSTS NOTHING. Every write piggybacks on a ListModels call that was
// already happening for another reason: the interactive model picker, the
// /provider-status health check, the startup reconcile. Nothing here initiates
// a request.
//
// A STALE CACHE IS STILL USED. Staleness decides only whether a refresh is
// worth doing; it never decides whether an answer is given. A day-old list is
// a far better basis for "which of your models can see" than a compiled-in
// constant from whenever the binary was built, which is what this replaces.
package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelCacheTTL is how long a list is considered fresh.
//
// A day, because model catalogues change on the scale of vendor announcements,
// not minutes — and because the penalty for being stale is only that a
// just-retired model is still offered, which the wire-level not-found handling
// catches anyway.
const ModelCacheTTL = 24 * time.Hour

// modelCacheVersion guards the file format. An unreadable or future version is
// treated as an empty cache rather than an error: this is a convenience store,
// and refusing to start because of it would be the wrong trade.
const modelCacheVersion = 1

// cachedList is one provider's remembered models.
type cachedList struct {
	FetchedAt time.Time   `json:"fetched_at"`
	Models    []ModelInfo `json:"models"`
}

// modelCacheFile is the on-disk shape.
type modelCacheFile struct {
	Version   int                   `json:"version"`
	Providers map[string]cachedList `json:"providers"`
}

// ModelCache is the process-wide model list store.
type ModelCache struct {
	mu   sync.RWMutex
	path string
	data modelCacheFile
}

var (
	defaultModelCacheOnce sync.Once
	defaultModelCache     *ModelCache
)

// Models returns the shared cache, reading ~/.helix/models.json on first use.
//
// Never returns nil: a cache that cannot be read or written still answers, it
// just answers "nothing known", which every caller already has to handle.
func Models() *ModelCache {
	defaultModelCacheOnce.Do(func() {
		defaultModelCache = newModelCacheAt(defaultModelCachePath())
	})
	return defaultModelCache
}

// ResetModelCacheForTest drops the process-wide cache so a test can point it
// at a temporary HOME. Tests only.
func ResetModelCacheForTest() {
	defaultModelCacheOnce = sync.Once{}
	defaultModelCache = nil
}

// defaultModelCachePath is ~/.helix/models.json, or "" when there is no home.
func defaultModelCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".helix", "models.json")
}

// newModelCacheAt loads a cache from a path, tolerating every failure.
func newModelCacheAt(path string) *ModelCache {
	c := &ModelCache{
		path: path,
		data: modelCacheFile{Version: modelCacheVersion, Providers: map[string]cachedList{}},
	}
	if path == "" {
		return c
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var parsed modelCacheFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return c
	}
	if parsed.Version != modelCacheVersion || parsed.Providers == nil {
		return c
	}
	c.data = parsed
	return c
}

// Get returns the remembered models for a provider, when it was fetched, and
// whether anything is known at all.
func (c *ModelCache) Get(provider string) ([]ModelInfo, time.Time, bool) {
	if c == nil {
		return nil, time.Time{}, false
	}
	key := strings.ToLower(strings.TrimSpace(provider))
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.data.Providers[key]
	if !ok || len(entry.Models) == 0 {
		return nil, time.Time{}, false
	}
	out := make([]ModelInfo, len(entry.Models))
	copy(out, entry.Models)
	return out, entry.FetchedAt, true
}

// Fresh reports whether a provider's list is within the TTL.
//
// Separate from Get on purpose: callers ASK for the list unconditionally and
// ask about freshness only to decide whether to spend a request refreshing it.
func (c *ModelCache) Fresh(provider string) bool {
	_, at, ok := c.Get(provider)
	return ok && time.Since(at) < ModelCacheTTL
}

// Put records a provider's model list and persists it, best-effort.
//
// An empty list is NOT recorded. A key with no model access, a transient 403,
// or a provider that answers 200 with nothing would otherwise overwrite a good
// list with nothing and make every model look retired.
func (c *ModelCache) Put(provider string, models []ModelInfo) error {
	if c == nil || len(models) == 0 {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(provider))
	if key == "" {
		return nil
	}

	stored := make([]ModelInfo, len(models))
	copy(stored, models)

	c.mu.Lock()
	if c.data.Providers == nil {
		c.data.Providers = map[string]cachedList{}
	}
	c.data.Providers[key] = cachedList{FetchedAt: time.Now(), Models: stored}
	path := c.path
	snapshot := c.data
	c.mu.Unlock()

	if path == "" {
		return nil
	}
	return writeModelCache(path, snapshot)
}

// Invalidate forgets a provider's list, so the next reconcile refetches.
//
// Called when the wire says a model is gone: the list that offered it is
// provably behind, and keeping it would let the same repair be attempted twice.
func (c *ModelCache) Invalidate(provider string) {
	if c == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(provider))
	c.mu.Lock()
	delete(c.data.Providers, key)
	path := c.path
	snapshot := c.data
	c.mu.Unlock()
	if path != "" {
		_ = writeModelCache(path, snapshot)
	}
}

// Knows reports whether a provider's remembered list contains a model ID.
//
// Case-insensitive, because vendors are inconsistent about casing in their own
// listings and a case difference is not a retirement.
//
// Absence is NOT proof of retirement — see the comment on Lists.
func (c *ModelCache) Knows(provider, modelID string) bool {
	want := strings.ToLower(strings.TrimSpace(modelID))
	if want == "" {
		return false
	}
	models, _, ok := c.Get(provider)
	if !ok {
		return false
	}
	for _, m := range models {
		if strings.ToLower(m.ID) == want {
			return true
		}
	}
	return false
}

// Lists reports whether anything is known about a provider at all.
//
// The distinction that keeps self-repair honest: "this provider's list does not
// contain your model" only means something if there IS a list. With no list,
// absence is ignorance, not evidence — and a repair driven by ignorance would
// replace a working model with a guess.
//
// Even with a list, absence only WARNS. Anthropic omits some aliases, Ollama's
// /api/tags shows only pulled models, and llama.cpp reports exactly one entry.
// Only a wire-level model-not-found repairs.
func (c *ModelCache) Lists(provider string) bool {
	_, _, ok := c.Get(provider)
	return ok
}

// Preferred returns the best model to use for a provider from what is known,
// or "" when nothing is.
func (c *ModelCache) Preferred(provider string) string {
	models, _, ok := c.Get(provider)
	if !ok {
		return ""
	}
	ranked := RankModels(provider, models)
	if len(ranked) == 0 {
		return ""
	}
	return ranked[0].ID
}

// AnyVisionModel reports whether a provider is known to serve a model that can
// see. Used by the provider-level capability floor.
func (c *ModelCache) AnyVisionModel(provider string) bool {
	models, _, ok := c.Get(provider)
	if !ok {
		return false
	}
	for _, m := range models {
		if SupportsVision(provider, m.ID) {
			return true
		}
	}
	return false
}

// writeModelCache persists the cache with the same permissions discipline as
// the keystore: 0700 directory, 0600 file. It holds no secrets, but it sits
// beside secrets.json and a looser mode there would be a surprise.
func writeModelCache(path string, data modelCacheFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create model cache directory: %w", err)
	}
	// Sorted keys so the file does not churn between writes that changed
	// nothing — a cache that produces a different byte stream every save is
	// noise in a directory users do look at.
	names := make([]string, 0, len(data.Providers))
	for name := range data.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := modelCacheFile{Version: modelCacheVersion, Providers: map[string]cachedList{}}
	for _, name := range names {
		ordered.Providers[name] = data.Providers[name]
	}

	raw, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model cache: %w", err)
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
