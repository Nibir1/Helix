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
