// internal/ai/vision_test.go
// Purpose: Phase 5 (P5.1/P5.5) — the vision capability gate fails closed for
// text-only models, and image parts reach the provider's Chat call.
package ai

import (
	"context"
	"testing"

	"helix/internal/providers"
)

type visionFakeProvider struct {
	vision bool
}

func (p *visionFakeProvider) Name() string        { return "fake" }
func (p *visionFakeProvider) DisplayName() string { return "Fake" }
func (p *visionFakeProvider) SetAPIKey(string)    {}
func (p *visionFakeProvider) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}
func (p *visionFakeProvider) HealthCheck(context.Context) error { return nil }
func (p *visionFakeProvider) RequiresAPIKey() bool              { return false }
func (p *visionFakeProvider) IsLocal() bool                     { return false }
func (p *visionFakeProvider) DefaultModel() string              { return "fake-model" }
func (p *visionFakeProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{Vision: p.vision}
}
func (p *visionFakeProvider) Chat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	ch := make(chan providers.StreamChunk, 2)
	reply := "no-image"
	if len(req.Messages) > 0 && req.Messages[0].HasImages() {
		reply = "image-arrived"
	}
	ch <- providers.StreamChunk{Content: reply}
	ch <- providers.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestRunVisionModelRefusesNonVisionProvider(t *testing.T) {
	oldProvider, oldModel := activeProvider, activeModel
	defer func() { activeProvider, activeModel = oldProvider, oldModel }()

	activeProvider = &visionFakeProvider{vision: false}
	activeModel = "fake-model"

	_, err := RunVisionModel("what's this", []providers.MessagePart{
		{Type: providers.PartImage, ImageData: []byte{1}},
	})
	if err == nil {
		t.Fatal("a text-only model must be refused the vision path")
	}
}

func TestRunVisionModelRoutesImagePart(t *testing.T) {
	oldProvider, oldModel := activeProvider, activeModel
	defer func() { activeProvider, activeModel = oldProvider, oldModel }()

	activeProvider = &visionFakeProvider{vision: true}
	activeModel = "fake-model"

	out, err := RunVisionModel("what's this", []providers.MessagePart{
		{Type: providers.PartImage, ImageData: []byte{1, 2, 3}},
	})
	if err != nil {
		t.Fatalf("vision call: %v", err)
	}
	if out != "image-arrived" {
		t.Fatalf("image part did not reach the provider: %q", out)
	}
}

// visionFake is a provider whose DEFAULT model differs from the model a user
// would select — the shape that exposed the bug.
type visionFake struct {
	providers.AIProvider
	name         string
	defaultModel string
}

func (f *visionFake) Name() string         { return f.name }
func (f *visionFake) DefaultModel() string { return f.defaultModel }
func (f *visionFake) IsLocal() bool        { return false }
func (f *visionFake) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor(f.name, f.defaultModel)
}

// VisionCapable must ask about the SELECTED model. It used to read
// activeProvider.Capabilities(), and every implementation of that computes its
// flags from the provider's DEFAULT model — so selecting gpt-4o asked "can the
// default model see?" and /eyes on refused on the answer.
func TestVisionCapableUsesTheSelectedModel(t *testing.T) {
	oldProvider, oldModel := activeProvider, activeModel
	t.Cleanup(func() { activeProvider, activeModel = oldProvider, oldModel })

	// Default model is text-only; the user selected a multimodal one.
	activeProvider = &visionFake{name: "openai", defaultModel: "gpt-3.5-turbo"}

	activeModel = "gpt-4o"
	if !VisionCapable() {
		t.Error("selecting gpt-4o must make vision available, whatever the provider default is")
	}

	activeModel = "gpt-3.5-turbo"
	if VisionCapable() {
		t.Error("a text-only selection must not report vision")
	}

	// No selection falls back to the provider default, which is the honest
	// answer when there is nothing else to go on.
	activeModel = ""
	if VisionCapable() {
		t.Error("with no selection the text-only default must not report vision")
	}
}

func TestVisionCapableWithNoProvider(t *testing.T) {
	oldProvider, oldModel := activeProvider, activeModel
	t.Cleanup(func() { activeProvider, activeModel = oldProvider, oldModel })

	activeProvider, activeModel = nil, "gpt-4o"
	if VisionCapable() {
		t.Error("no provider means no vision, whatever the model string says")
	}
}
