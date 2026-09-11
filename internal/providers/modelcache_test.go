// internal/providers/modelcache_test.go
// Purpose: the cache's guarantees, especially the two that keep self-repair
// honest — an empty list never overwrites a good one, and "not in the list" is
// only meaningful when there IS a list.
package providers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// osWriteFile is os.WriteFile, named so the corrupt-file helper reads clearly.
func osWriteFile(path string, b []byte) error { return os.WriteFile(path, b, 0o600) }

func tempCache(t *testing.T) *ModelCache {
	t.Helper()
	return newModelCacheAt(filepath.Join(t.TempDir(), "models.json"))
}

func TestModelCacheRoundTripsThroughDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")

	c := newModelCacheAt(path)
	if err := c.Put("deepseek", []ModelInfo{{ID: "deepseek-chat"}, {ID: "deepseek-v4-flash-vision-exp"}}); err != nil {
		t.Fatalf("put: %v", err)
	}

	reloaded := newModelCacheAt(path)
	models, at, ok := reloaded.Get("deepseek")
	if !ok {
		t.Fatal("nothing survived the round trip")
	}
	if len(models) != 2 {
		t.Errorf("got %d models, want 2", len(models))
	}
	if time.Since(at) > time.Minute {
		t.Errorf("fetched_at did not survive: %v", at)
	}
	if !reloaded.Fresh("deepseek") {
		t.Error("a list written a moment ago is not fresh")
	}
}

// An empty list must never overwrite a good one. A key with no model access, a
// transient 403, or a 200 with an empty array would otherwise make every model
// the user has look retired — and self-repair would act on that.
func TestEmptyListNeverOverwritesAGoodOne(t *testing.T) {
	c := tempCache(t)
	if err := c.Put("openai", []ModelInfo{{ID: "gpt-5.6-luna"}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := c.Put("openai", nil); err != nil {
		t.Fatalf("put empty: %v", err)
	}
	if !c.Knows("openai", "gpt-5.6-luna") {
		t.Error("an empty list erased a known model — every model would look retired")
	}
}

// The distinction self-repair depends on: with no list, absence is ignorance
// rather than evidence, and a repair driven by ignorance replaces a working
// model with a guess.
func TestAbsenceOnlyMeansSomethingWhenThereIsAList(t *testing.T) {
	c := tempCache(t)
	if c.Lists("kimi") {
		t.Fatal("an untouched provider claims to have a list")
	}
	if c.Knows("kimi", "kimi-k3") {
		t.Error("a provider with no list claimed to know a model")
	}

	if err := c.Put("kimi", []ModelInfo{{ID: "kimi-k3"}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !c.Lists("kimi") {
		t.Error("a provider with a list denies having one")
	}
	if c.Knows("kimi", "kimi-k2-retired") {
		t.Error("a retired model was reported as known")
	}
}

// Vendors are inconsistent about casing in their own listings, and a case
// difference is not a retirement.
func TestKnowsIsCaseInsensitive(t *testing.T) {
	c := tempCache(t)
	_ = c.Put("xai", []ModelInfo{{ID: "Grok-4.6"}})
	if !c.Knows("xai", "grok-4.6") {
		t.Error("a case difference read as a different model")
	}
}

// Preferred is what replaces the hardcoded default, so it has to apply the
// same vision-and-fast-first ranking rather than taking the vendor's order.
func TestPreferredAppliesTheRanking(t *testing.T) {
	c := tempCache(t)
	_ = c.Put("openai", []ModelInfo{
		{ID: "text-embedding-3-small"},
		{ID: "gpt-4o"},
		{ID: "gpt-5.6-luna-mini"},
	})
	if got := c.Preferred("openai"); got != "gpt-5.6-luna-mini" {
		t.Errorf("Preferred = %q, want the vision+fast build", got)
	}
	if got := c.Preferred("nothing-known"); got != "" {
		t.Errorf("Preferred on an unknown provider = %q, want empty", got)
	}
}

func TestAnyVisionModel(t *testing.T) {
	c := tempCache(t)
	_ = c.Put("seeing", []ModelInfo{{ID: "some-vision-build"}})
	_ = c.Put("blind", []ModelInfo{{ID: "local-gguf"}})
	if !c.AnyVisionModel("seeing") {
		t.Error("a provider listing a vision build was reported blind")
	}
	if c.AnyVisionModel("blind") {
		t.Error("a provider with no vision build was reported sighted")
	}
	if c.AnyVisionModel("unknown") {
		t.Error("an unknown provider claimed vision")
	}
}

// The wire said a model is gone, so the list that offered it is provably
// behind and must not drive a second repair attempt.
func TestInvalidateForgetsTheList(t *testing.T) {
	c := tempCache(t)
	_ = c.Put("glm", []ModelInfo{{ID: "glm-5.3-flash"}})
	c.Invalidate("glm")
	if c.Lists("glm") {
		t.Error("the stale list survived invalidation")
	}
}

// A cache that cannot be read must still answer. Refusing to start because of
// a convenience file would be the wrong trade.
func TestUnreadableCacheStillAnswers(t *testing.T) {
	c := newModelCacheAt("")
	if c == nil {
		t.Fatal("a cache with no path is nil")
	}
	if c.Lists("openai") {
		t.Error("a pathless cache claims to know something")
	}
	if err := c.Put("openai", []ModelInfo{{ID: "x"}}); err != nil {
		t.Errorf("a pathless put errored: %v", err)
	}
	if !c.Knows("openai", "x") {
		t.Error("a pathless cache did not remember in-process")
	}
}

func TestCorruptCacheFileIsTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := writeCorrupt(path); err != nil {
		t.Fatal(err)
	}
	c := newModelCacheAt(path)
	if c.Lists("openai") {
		t.Error("a corrupt cache produced a list")
	}
}

// writeCorrupt puts something that is not this file format at path.
func writeCorrupt(path string) error {
	return osWriteFile(path, []byte("{not json"))
}
