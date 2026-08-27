// internal/speech/piper_native_test.go
// Purpose: Piper was the one component that forced a Python interpreter on a
// project built around a CGO-free Go binary. These pin the escape route and,
// more importantly, the reason macOS does not get it.
package speech

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// macOS is excluded ON PURPOSE and the reason must survive: both macOS archives
// in the pinned release ship the libonnxruntime .dSYM debug bundle and NO
// .dylib, so the extracted binary dies on
// "dyld: Library not loaded: @rpath/libespeak-ng.1.dylib".
//
// Verified by downloading and running it, not inferred from the file list.
// Offering 19 MB that produces an executable which cannot start is the exact
// "walked into something that cannot work" failure the sidecar preconditions
// exist to prevent.
func TestPiperBinaryIsNotOfferedOnMacOS(t *testing.T) {
	asset, ok := PiperReleaseAsset()

	if runtime.GOOS == "darwin" {
		if ok {
			t.Errorf("macOS must not be offered the standalone binary, got %q", asset)
		}
		reason := PiperNativeUnavailableReason()
		if !strings.Contains(reason, "libraries") {
			t.Errorf("the reason must name the actual defect, got %q", reason)
		}
		if !strings.Contains(reason, PiperReleaseVersion) {
			t.Errorf("the reason must name the release it applies to, got %q", reason)
		}
		return
	}

	// Every other platform Helix targets has a verified-complete archive:
	// Linux carries libespeak-ng.so and libpiper_phonemize.so, Windows carries
	// the four matching DLLs.
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/arm64", "linux/arm", "windows/amd64":
		if !ok {
			t.Errorf("%s/%s has a working published build and should be offered it",
				runtime.GOOS, runtime.GOARCH)
		}
		if !strings.Contains(asset, "piper") {
			t.Errorf("asset name looks wrong: %q", asset)
		}
	}
}

// A reason must always be available when no asset is, or the caller has nothing
// to tell the user.
func TestPiperUnavailableAlwaysExplainsItself(t *testing.T) {
	if _, ok := PiperReleaseAsset(); !ok {
		if strings.TrimSpace(PiperNativeUnavailableReason()) == "" {
			t.Error("no binary offer and no explanation is the worst of both")
		}
	}
}

// The native adapter must refuse clearly rather than shelling out with an empty
// model path, which produces an inscrutable piper usage dump.
func TestPiperNativeRefusesWithoutAVoice(t *testing.T) {
	p := NewPiperNativeTTS("/nonexistent/piper", "")
	_, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err == nil {
		t.Fatal("synthesis without a voice model must fail")
	}
	if !strings.Contains(err.Error(), "voice model") {
		t.Errorf("the error should name what is missing, got %v", err)
	}
}

// Identity must match the HTTP adapter's: the transport is an implementation
// detail, and every preset, pricing row and failover chain names "piper-local".
func TestPiperNativeSharesTheProviderName(t *testing.T) {
	native := NewPiperNativeTTS("/opt/piper/piper", "/voices/en.onnx")
	http := NewPiperTTS("http://127.0.0.1:5000")

	if native.Name() != http.Name() {
		t.Errorf("native %q and http %q must be the same provider to the rest of Helix",
			native.Name(), http.Name())
	}
	if !native.IsLocal() || native.RequiresAPIKey() {
		t.Error("the native path is local and keyless")
	}
}
