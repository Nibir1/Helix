// internal/ai/reconcile_test.go
// Purpose: self-repair must fire on evidence and stay silent without it.
//
// The bug being fixed: nothing validated the saved model, so a vendor
// retirement meant every turn returned 404 forever while /provider-status
// reported ok. The danger in fixing it is the opposite failure — repairing on
// a dropped connection and replacing a working configuration with a guess.
package ai

import (
	"context"
	"errors"
	"testing"

	"helix/internal/providers"
)

// reconcileFake is an AIProvider whose catalogue the test controls.
type reconcileFake struct {
	name   string
	local  bool
	models []providers.ModelInfo
	err    error
	calls  int
}

func (p *reconcileFake) Name() string        { return p.name }
func (p *reconcileFake) DisplayName() string { return p.name }
func (p *reconcileFake) SetAPIKey(string)    {}
func (p *reconcileFake) ListModels(context.Context) ([]providers.ModelInfo, error) {
	p.calls++
	return p.models, p.err
}
func (p *reconcileFake) HealthCheck(context.Context) error { return nil }
func (p *reconcileFake) RequiresAPIKey() bool              { return false }
func (p *reconcileFake) IsLocal() bool                     { return p.local }
func (p *reconcileFake) DefaultModel() string              { return "" }
func (p *reconcileFake) Capabilities() providers.Capabilities {
	return providers.Capabilities{Chat: true}
}
func (p *reconcileFake) Chat(context.Context, providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	ch := make(chan providers.StreamChunk)
	close(ch)
	return ch, nil
}

// withReconcileFake installs a fake as the active provider for one test.
func withReconcileFake(t *testing.T, f *reconcileFake, model string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	providers.ResetModelCacheForTest()

	prevProvider, prevModel, prevChoices := activeProvider, activeModel, userModelChoices
	t.Cleanup(func() {
		activeProvider, activeModel, userModelChoices = prevProvider, prevModel, prevChoices
		providers.ResetModelCacheForTest()
	})
	activeProvider, activeModel = f, model
	userModelChoices = map[string]string{}
}

// The reported failure: the vendor retired the saved model. The provider lists
// a catalogue, the saved ID is not in it, so repair — and say which verdict it
// was, because that is what decides whether the user is told.
func TestReconcileRepairsARetiredModel(t *testing.T) {
	f := &reconcileFake{name: "deepseek", models: []providers.ModelInfo{
		{ID: "deepseek-v4-flash-vision-exp"},
		{ID: "deepseek-chat"},
	}}
	withReconcileFake(t, f, "deepseek-v3-retired")

	got, changed, verdict := ReconcileActiveModel(context.Background())
	if !changed {
		t.Fatal("a retired model was left in place — every turn would keep 404ing")
	}
	if verdict != ModelGone {
		t.Errorf("verdict = %v, want ModelGone", verdict)
	}
	if got != "deepseek-v4-flash-vision-exp" {
		t.Errorf("repaired to %q, want the best-ranked replacement", got)
	}
	if ActiveModel() != got {
		t.Errorf("the active model was not updated: %q", ActiveModel())
	}
}

// THE guard against over-eager repair. An unreachable provider is not a vendor
// retiring a model, and acting on it would replace a working configuration
// with a guess on every flaky network.
func TestReconcileKeepsTheModelWhenItCannotCheck(t *testing.T) {
	f := &reconcileFake{name: "openai", err: errors.New("dial tcp: connection refused")}
	withReconcileFake(t, f, "gpt-5.6-luna")

	got, changed, verdict := ReconcileActiveModel(context.Background())
	if changed {
		t.Error("an unreachable provider triggered a repair")
	}
	if verdict != ModelUnverifiable {
		t.Errorf("verdict = %v, want ModelUnverifiable", verdict)
	}
	if got != "gpt-5.6-luna" {
		t.Errorf("model = %q, want it untouched", got)
	}
}

// An empty catalogue is the same class of non-evidence: a key without model
// access returns 200 and nothing, and repairing on that would make every
// model look retired.
func TestReconcileTreatsAnEmptyCatalogueAsUnknown(t *testing.T) {
	f := &reconcileFake{name: "kimi", models: nil}
	withReconcileFake(t, f, "kimi-k3")

	_, changed, verdict := ReconcileActiveModel(context.Background())
	if changed || verdict != ModelUnverifiable {
		t.Errorf("changed=%v verdict=%v, want no repair on an empty catalogue",
			changed, verdict)
	}
}

// A model that is not PULLED is a pull away, not a retirement. Silently
// switching would pick a different brain than the user asked for; the
// interactive path offers to fetch it instead.
func TestReconcileDoesNotSwitchLocalModels(t *testing.T) {
	f := &reconcileFake{name: "ollama", local: true, models: []providers.ModelInfo{
		{ID: "gemma4:e2b"},
	}}
	withReconcileFake(t, f, "llama3.1:8b")

	got, changed, _ := ReconcileActiveModel(context.Background())
	if changed {
		t.Errorf("switched a local model to %q — it needs pulling, not replacing", got)
	}
}

// A model the catalogue confirms must be left alone, and the cache must make
// the second check free.
func TestReconcileLeavesAListedModelAloneAndCaches(t *testing.T) {
	f := &reconcileFake{name: "glm", models: []providers.ModelInfo{{ID: "glm-5.3-flash"}}}
	withReconcileFake(t, f, "glm-5.3-flash")

	if _, changed, verdict := ReconcileActiveModel(context.Background()); changed || verdict != ModelOK {
		t.Errorf("changed=%v verdict=%v, want a listed model untouched", changed, verdict)
	}
	first := f.calls
	if _, _, _ = ReconcileActiveModel(context.Background()); f.calls != first {
		t.Errorf("the second reconcile spent a request: %d -> %d", first, f.calls)
	}
}

// With nothing selected the shell cannot answer anything, so picking is
// strictly better than leaving it empty.
func TestReconcilePicksAModelWhenNoneIsSelected(t *testing.T) {
	f := &reconcileFake{name: "xai", models: []providers.ModelInfo{{ID: "grok-4.6"}}}
	withReconcileFake(t, f, "")
	_ = providers.Models().Put("xai", f.models)

	got, changed, _ := ReconcileActiveModel(context.Background())
	if !changed || got != "grok-4.6" {
		t.Errorf("got %q changed=%v, want a model picked", got, changed)
	}
}

// The user's explicit choice for a provider outranks the ranking, or /model use
// would be undone by the next resolution.
func TestUserChoiceOutranksTheRanking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	providers.ResetModelCacheForTest()
	t.Cleanup(providers.ResetModelCacheForTest)

	prev := userModelChoices
	t.Cleanup(func() { userModelChoices = prev })

	_ = providers.Models().Put("openai", []providers.ModelInfo{
		{ID: "gpt-5.6-luna-mini"}, {ID: "gpt-4o"},
	})
	SetUserModelChoices(map[string]string{"openai": "gpt-4o"})
	if got := PreferredModel("openai"); got != "gpt-4o" {
		t.Errorf("PreferredModel = %q, want the user's choice", got)
	}

	SetUserModelChoices(nil)
	if got := PreferredModel("openai"); got != "gpt-5.6-luna-mini" {
		t.Errorf("with no choice = %q, want the best-ranked model", got)
	}
}
