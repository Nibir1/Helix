// internal/wakeword/bench_test.go
// Purpose: Phase 3's Risks line — "continuous capture CPU (chunk size + sleep
// intervals; measure and record % CPU in dev log)". The measurement was never
// taken. Phase 6 benchmarked the ambient analyzer (26µs/chunk) and the wake
// detector — which runs on every chunk of a permanently-open microphone — never
// got the same treatment.
//
// Scope, stated because it bounds what the number means: this measures the
// DETECTION cost per chunk, not capture. sox records in another process and its
// cost is not Helix's to benchmark; what Helix controls is how much CPU it burns
// deciding whether each chunk was a wake, and that is the part that runs forever.
package wakeword

import (
	"testing"

	"helix/internal/speech"
)

// benchChunkSamples is one wake-scan chunk at the shipped defaults: 1500ms at
// 16kHz mono, matching SpeechWakeConfig.ChunkMs and the sox scanner.
const (
	benchRate         = 16000
	benchChunkMs      = 1500
	benchChunkSamples = benchRate * benchChunkMs / 1000
)

// benchSpeechChunk is a speech-level tone: loud enough that the detector does
// its full arithmetic instead of short-circuiting.
func benchSpeechChunk() speech.AudioFormat {
	return speech.AudioFormat{
		Kind:       speech.KindPCM,
		SampleRate: benchRate,
		Channels:   1,
		Bytes:      pcmTone(benchChunkSamples, 0.2, 220, benchRate),
	}
}

// benchSilentChunk is the quiet room — the state the loop spends almost all of
// its life in.
func benchSilentChunk() speech.AudioFormat {
	return speech.AudioFormat{
		Kind:       speech.KindPCM,
		SampleRate: benchRate,
		Channels:   1,
		Bytes:      make([]byte, benchChunkSamples*2),
	}
}

// BenchmarkEnergyWake measures the per-chunk cost of the shipped default engine.
// Divide by the chunk duration for the duty cycle.
func BenchmarkEnergyWake(b *testing.B) {
	d := NewEnergyDetector(PresetBalanced)
	clip := benchSpeechChunk()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = d.Wake(clip)
	}
}

// BenchmarkEnergyWakeSilence is the common case: detection should be cheapest
// exactly when nothing is happening, which is when it runs most.
func BenchmarkEnergyWakeSilence(b *testing.B) {
	d := NewEnergyDetector(PresetBalanced)
	clip := benchSilentChunk()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = d.Wake(clip)
	}
}

// BenchmarkEnergyWakeWAV covers the other input shape the scanner can produce,
// since WAV adds a header parse per chunk.
func BenchmarkEnergyWakeWAV(b *testing.B) {
	d := NewEnergyDetector(PresetBalanced)
	clip := wavFromPCM(pcmTone(benchChunkSamples, 0.2, 220, benchRate), benchRate)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = d.Wake(clip)
	}
}

// TestEnergyWakeDutyCycleIsNegligible turns the Risks-line measurement into an
// assertion, so it cannot quietly regress.
//
// The budget is deliberately loose (1% of real time). The point is not to pin a
// number on one machine; it is to fail if detection ever becomes expensive
// enough to matter on a Raspberry Pi, where this loop runs continuously and
// shares the board with a local model.
func TestEnergyWakeDutyCycleIsNegligible(t *testing.T) {
	d := NewEnergyDetector(PresetBalanced)
	clip := benchSpeechChunk()

	res := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _, _ = d.Wake(clip)
		}
	})
	if res.N == 0 {
		t.Skip("benchmark did not run")
	}

	perChunkNs := res.T.Nanoseconds() / int64(res.N)
	const chunkNs = int64(benchChunkMs) * 1000 * 1000
	dutyPercent := float64(perChunkNs) / float64(chunkNs) * 100

	t.Logf("energy detection: %dns per %dms chunk = %.5f%% duty cycle (%d iterations)",
		perChunkNs, benchChunkMs, dutyPercent, res.N)

	if dutyPercent > 1.0 {
		t.Errorf("wake detection duty cycle %.3f%% exceeds the 1%% budget — "+
			"continuous capture would be a measurable drain on a small board",
			dutyPercent)
	}
}
