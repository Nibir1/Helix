// internal/speech/fuzz_test.go
// Purpose: Phase 7 (P7.5) — fuzz the tolerant WAV header parser (deferred
// from P1.11). Invariant: never panic, and a successful parse always returns
// non-negative sample rate / channels.
package speech

import "testing"

func FuzzWAVHeaderInfo(f *testing.F) {
	f.Add([]byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00"))
	f.Add([]byte{})
	f.Add([]byte("RIFF"))
	f.Add([]byte("not-a-wav-file-at-all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		rate, channels, err := wavHeaderInfo(data)
		if err == nil && (rate < 0 || channels < 0) {
			t.Fatalf("successful parse returned negative values: rate=%d channels=%d", rate, channels)
		}
	})
}
