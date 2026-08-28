package ollama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStore builds an Ollama-shaped model store: manifests referencing
// content-addressed blobs, exactly as `ollama pull` leaves them.
func fakeStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("OLLAMA_MODELS", root)
	return root
}

type layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func writeModel(t *testing.T, root, namespace, model, tag string, blobBytes int, withBlob bool) string {
	t.Helper()

	// A digest is 64 hex chars; derive a deterministic, valid-length one from the
	// model+tag so each fixture gets its own blob.
	seed := model + tag
	var hex strings.Builder
	for _, r := range seed {
		fmt.Fprintf(&hex, "%02x", byte(r))
	}
	digestHex := (hex.String() + strings.Repeat("0", 64))[:64]
	digest := "sha256:" + digestHex

	manifest := map[string]any{
		"layers": []layer{
			{MediaType: "application/vnd.ollama.image.template", Digest: "sha256:aaa", Size: 10},
			{MediaType: ggufMediaType, Digest: digest, Size: int64(blobBytes)},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "manifests", "registry.ollama.ai", namespace, model)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tag), data, 0o644); err != nil {
		t.Fatal(err)
	}

	blobPath := filepath.Join(root, "blobs", "sha256-"+digestHex)
	if withBlob {
		if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blobPath, make([]byte, blobBytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return blobPath
}

func TestLocalGGUFsFindsPulledModels(t *testing.T) {
	root := fakeStore(t)
	smallPath := writeModel(t, root, "library", "gemma4", "e2b", 2048, true)
	bigPath := writeModel(t, root, "library", "llama3.1", "8b", 8192, true)

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d models, want 2: %+v", len(got), got)
	}

	// Largest first, so the most capable model is the obvious suggestion.
	if got[0].Name != "llama3.1:8b" || got[0].Path != bigPath {
		t.Errorf("first entry = %+v, want llama3.1:8b at %s", got[0], bigPath)
	}
	if got[1].Name != "gemma4:e2b" || got[1].Path != smallPath {
		t.Errorf("second entry = %+v", got[1])
	}
	if got[0].SizeBytes != 8192 {
		t.Errorf("size = %d, want 8192", got[0].SizeBytes)
	}
}

// TestLocalGGUFsNamesNonLibraryNamespaces: a model from a user namespace must
// keep it, or the name will not match `ollama list`.
func TestLocalGGUFsNamesNonLibraryNamespaces(t *testing.T) {
	root := fakeStore(t)
	writeModel(t, root, "someuser", "custommodel", "latest", 1024, true)

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d models, want 1", len(got))
	}
	if got[0].Name != "someuser/custommodel:latest" {
		t.Errorf("name = %q, want someuser/custommodel:latest", got[0].Name)
	}
}

// TestLocalGGUFsSkipsMissingBlobs: a manifest whose blob was garbage-collected
// must not be offered, because the path it names does not exist.
func TestLocalGGUFsSkipsMissingBlobs(t *testing.T) {
	root := fakeStore(t)
	writeModel(t, root, "library", "present", "latest", 512, true)
	writeModel(t, root, "library", "collected", "latest", 512, false)

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d models, want only the one with a blob: %+v", len(got), got)
	}
	if got[0].Name != "present:latest" {
		t.Errorf("name = %q", got[0].Name)
	}
	if _, err := os.Stat(got[0].Path); err != nil {
		t.Errorf("a listed path must exist: %v", err)
	}
}

// TestLocalGGUFsSkipsManifestsWithoutModelLayer: an embedding-only or template
// manifest carries no weights.
func TestLocalGGUFsSkipsManifestsWithoutModelLayer(t *testing.T) {
	root := fakeStore(t)
	dir := filepath.Join(root, "manifests", "registry.ollama.ai", "library", "weird")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"layers":[{"mediaType":"application/vnd.ollama.image.license","digest":"sha256:x","size":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "latest"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a manifest with no model layer must be skipped: %+v", got)
	}
}

// TestLocalGGUFsMissingStoreIsNotAnError: never having pulled anything is a
// normal state, not a failure.
func TestLocalGGUFsMissingStoreIsNotAnError(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", filepath.Join(t.TempDir(), "absent"))

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatalf("a missing store must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no models, got %+v", got)
	}
}

func TestLocalGGUFsToleratesCorruptManifest(t *testing.T) {
	root := fakeStore(t)
	writeModel(t, root, "library", "good", "latest", 256, true)

	dir := filepath.Join(root, "manifests", "registry.ollama.ai", "library", "bad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "latest"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LocalGGUFs()
	if err != nil {
		t.Fatalf("one corrupt manifest must not fail the listing: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good:latest" {
		t.Errorf("healthy models must still be listed: %+v", got)
	}
}

func TestModelsDirHonorsEnv(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", "/custom/store")
	got, err := ModelsDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/store" {
		t.Errorf("ModelsDir = %q, want /custom/store", got)
	}

	t.Setenv("OLLAMA_MODELS", "")
	got, err = ModelsDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "models" || !filepath.IsAbs(got) {
		t.Errorf("default ModelsDir = %q, want an absolute path ending in models", got)
	}
}

func TestSizeGB(t *testing.T) {
	m := LocalModel{SizeBytes: 4 << 30}
	if got := m.SizeGB(); got != 4 {
		t.Errorf("SizeGB = %v, want 4", got)
	}
}
