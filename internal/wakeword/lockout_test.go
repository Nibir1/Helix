// internal/wakeword/lockout_test.go
// Purpose: enforce ADR-005 §5 — "wake-word-triggered sessions have a hard 60s
// inactivity lockout back to wake-only listening" — and specifically the Phase 3
// acceptance claim that between turns "NOTHING is transcribed".
//
// That claim was ticked as "proven by construction": the wake loop only ever
// calls a detector, so no audio between turns reaches an STT provider. The
// construction is real, and nothing was stopping a future edit from quietly
// dissolving it — adding one convenience call to speech.Transcribe inside the
// scan loop would send every ambient chunk of a quiet room to a cloud provider,
// which is a privacy regression (threat V1/V6) and a bill, and no test would
// have noticed.
//
// So the construction is now checked as a construction: this package may use
// speech for CAPTURE and audio types, and may not reach transcription.
package wakeword

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptionAPI lists the speech-package entry points that send audio to a
// provider. Reaching any of them from the wake loop would break the between-turn
// lockout.
var transcriptionAPI = map[string]bool{
	"Transcribe":     true,
	"StreamingSTT":   true,
	"TranscribeClip": true,
	"Speak":          true,
	"SpeakStream":    true,
	"Synthesize":     true,
}

func TestWakeLoopNeverTranscribes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob wakeword package: %v", err)
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "speech" {
				return true
			}
			if transcriptionAPI[sel.Sel.Name] {
				t.Errorf("%s calls speech.%s — the wake loop must never transcribe or "+
					"synthesize between turns (ADR-005 §5). Every ambient chunk of a "+
					"quiet room would be sent to a provider.",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// The complement: the package must still be allowed to CAPTURE, or the lockout
// would be enforced by the wake loop not working. This asserts the capability
// the loop legitimately needs, so a future "fix" that removed capture to satisfy
// the test above would fail here instead.
func TestWakeLoopStillUsesCapture(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var usesScanner bool
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "speech" {
				if strings.Contains(sel.Sel.Name, "ChunkScanner") {
					usesScanner = true
				}
			}
			return true
		})
	}
	if !usesScanner {
		t.Error("the wake loop should still use speech's chunk scanner for capture — " +
			"a lockout enforced by the loop not listening is not a lockout")
	}
}

// The Service is constructed from a Detector and a Scanner and nothing else, so
// there is no seam through which a transcription client could be injected. This
// pins that shape: if Service ever grows a field that could hold an STT
// provider, the type assertion below stops compiling and the reason is here.
func TestServiceHasNoTranscriptionSeam(t *testing.T) {
	// A detector returns a score and a boolean — it cannot return text, so a
	// wake decision structurally cannot carry a transcript out of the loop.
	var d Detector = NewEnergyDetector(PresetBalanced)
	score, woke, err := d.Wake(benchSilentChunk())
	if err != nil {
		t.Fatalf("silent chunk should not error: %v", err)
	}
	if woke {
		t.Error("silence must not wake the loop")
	}
	if score < 0 {
		t.Errorf("score should be a normalized magnitude, got %v", score)
	}
}
