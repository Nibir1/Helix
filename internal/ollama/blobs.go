// internal/ollama/blobs.go
// Purpose: locate the GGUF files Ollama has already downloaded, so llama.cpp can
// serve them without a second copy.
//
// Why this belongs here: "can llama.cpp use my Ollama models?" has a good answer
// — yes — but acting on it requires knowing where the weights live, and Ollama
// stores them content-addressed, as `blobs/sha256-<hex>` with no extension. The
// model NAME lives separately, in a manifest. Nobody is going to reverse that by
// hand, so Helix reads both and prints the mapping.
//
// The layout (Ollama's on-disk format):
//
//	$OLLAMA_MODELS or ~/.ollama/models/
//	  manifests/<registry>/<namespace>/<model>/<tag>   JSON: layers with digests
//	  blobs/sha256-<hex>                               the actual files
//
// The layer whose mediaType is application/vnd.ollama.image.model is the GGUF.
// llama.cpp identifies GGUF by magic bytes rather than filename, so that blob
// path can be handed straight to `llama-server -m`.
package ollama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ggufMediaType marks the layer holding model weights.
const ggufMediaType = "application/vnd.ollama.image.model"

// LocalModel is one Ollama-pulled model with the path to its GGUF weights.
type LocalModel struct {
	// Name is the tag as Ollama shows it ("llama3.1:8b").
	Name string

	// Path is the absolute blob path, suitable for `llama-server -m`.
	Path string

	// SizeBytes is the weight file's size on disk.
	SizeBytes int64
}

// SizeGB renders the size the way a model listing does.
func (m LocalModel) SizeGB() float64 {
	return float64(m.SizeBytes) / (1 << 30)
}

// ModelsDir returns Ollama's model store, honoring OLLAMA_MODELS.
func ModelsDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("OLLAMA_MODELS")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama", "models"), nil
}

// LocalGGUFs lists the models Ollama has on disk, newest-largest first.
//
// A missing store is not an error — it just means Ollama has never pulled
// anything here, which is a normal state worth reporting as "none" rather than
// as a failure.
func LocalGGUFs() ([]LocalModel, error) {
	root, err := ModelsDir()
	if err != nil {
		return nil, err
	}
	manifests := filepath.Join(root, "manifests")
	if _, err := os.Stat(manifests); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []LocalModel
	// A manifest path is <registry>/<namespace>/<model>/<tag>, so the tag is the
	// leaf and the model name is its parent. Walking is simpler than assuming a
	// fixed depth, because registry and namespace both vary.
	walkErr := filepath.WalkDir(manifests, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree must not hide the readable ones
		}
		if d.IsDir() {
			return nil
		}
		name, blobPath, size, ok := readManifest(root, manifests, path)
		if !ok {
			return nil
		}
		out = append(out, LocalModel{Name: name, Path: blobPath, SizeBytes: size})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// readManifest extracts the model name and GGUF blob path from one manifest
// file, reporting whether it held a usable model layer.
func readManifest(root, manifestRoot, path string) (name, blobPath string, size int64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, false
	}

	var manifest struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", 0, false
	}

	digest := ""
	for _, l := range manifest.Layers {
		if l.MediaType == ggufMediaType {
			digest = l.Digest
			size = l.Size
			break
		}
	}
	if digest == "" {
		return "", "", 0, false
	}

	blobPath = filepath.Join(root, "blobs", strings.ReplaceAll(digest, ":", "-"))
	info, err := os.Stat(blobPath)
	if err != nil {
		// The manifest references a blob that is not there — a partial or
		// garbage-collected pull. Listing it would send the user to a path that
		// does not exist.
		return "", "", 0, false
	}
	if size <= 0 {
		size = info.Size()
	}

	return manifestName(manifestRoot, path), blobPath, size, true
}

// manifestName turns a manifest file path back into the tag Ollama displays.
//
// "library" is Ollama's default namespace and is elided in its own output, so
// "library/llama3.1/8b" is shown as "llama3.1:8b" — matching it keeps the names
// recognizable against `ollama list`.
func manifestName(manifestRoot, path string) string {
	rel, err := filepath.Rel(manifestRoot, path)
	if err != nil {
		return filepath.Base(path)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return filepath.Base(path)
	}

	tag := parts[len(parts)-1]
	model := parts[len(parts)-2]
	namespace := ""
	if len(parts) >= 3 {
		namespace = parts[len(parts)-3]
	}

	if namespace != "" && namespace != "library" {
		return fmt.Sprintf("%s/%s:%s", namespace, model, tag)
	}
	return fmt.Sprintf("%s:%s", model, tag)
}
