// internal/llamacpp/models_test.go
package llamacpp

import "testing"

func TestRecommendedModels(t *testing.T) {
	models := RecommendedModels()

	if len(models) == 0 {
		t.Fatal("expected recommended models")
	}
}

func TestFindModel(t *testing.T) {
	model, ok := FindModel("tinyllama-1-1b")
	if !ok {
		t.Fatal("expected to find tinyllama-1-1b")
	}

	if model.URL == "" {
		t.Fatal("expected model URL")
	}
}
