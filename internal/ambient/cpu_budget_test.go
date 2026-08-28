// internal/ambient/cpu_budget_test.go
// Purpose: P6.4's promised "CPU budget test (<5% idle overhead on dev laptop,
// logged)". Only a benchmark existed, and its own comment said it was there "so
// the CPU budget can be evidenced" — it enabled the check and never performed
// it. Nothing computed the percentage, so the <5% claim had never been tested.
//
// It also measured the wrong thing. BenchmarkAnalyzer analyzes a 1024-sample
// window, which is 64ms of audio; production calls ChunkMonitor.Observe on a
// whole wake-loop chunk — 1500ms, 24000 samples at 16kHz — which additionally
// decodes a WAV and pads the FFT up to 32768 points. That is roughly fifty times
// the transform work per call, so the existing benchmark could not support a
// statement about idle overhead even after someone did the arithmetic.
//
// Ambient runs on a permanently-open microphone, tee'd off the wake stream, so
// this duty cycle is paid continuously for as long as live mode is on.
package ambient

import (
	"math"
	"testing"
	"time"

	"helix/internal/speech"
)

// Production chunk geometry: the wake scanner's default chunk, which is what
// TeeScanner hands to Observe.
const (
	budgetRate      = 16000
	budgetChunkMs   = 1500
	budgetChunkLen  = budgetRate * budgetChunkMs / 1000
	budgetChunkSpan = budgetChunkMs * time.Millisecond
)

// realisticChunk builds a 1500ms WAV chunk with speech-level broadband content,
// so the analyzer does its full arithmetic rather than short-circuiting on
// silence. WAV, not raw samples: decoding is part of what production pays.
func realisticChunk() speech.AudioFormat {
	pcm := make([]byte, budgetChunkLen*2)
	// A couple of tones plus a slow amplitude sweep — enough spectral content
	// that concentration is genuinely computed.
	for i := 0; i < budgetChunkLen; i++ {
		t := float64(i) / budgetRate
		v := 0.12*math.Sin(2*math.Pi*220*t) +
			0.06*math.Sin(2*math.Pi*760*t) +
			0.03*math.Sin(2*math.Pi*3100*t)
		s := int16(v * 32767)
		pcm[2*i] = byte(s)
		pcm[2*i+1] = byte(s >> 8)
	}
	return speech.AudioFormat{
		Kind:       speech.KindWAV,
		SampleRate: budgetRate,
		Channels:   1,
		Bytes:      speech.EncodeWAVPCM16(pcm, budgetRate, 1),
	}
}

// BenchmarkChunkMonitorObserve measures the PRODUCTION path: WAV decode plus
// analysis of one full wake-loop chunk, exactly what TeeScanner triggers.
func BenchmarkChunkMonitorObserve(b *testing.B) {
	mon := NewChunkMonitor(NewServiceFromOptions(0.5, ResponseLog, map[Category]bool{
		CategoryLoudNoise: true, CategoryAlarmLike: true,
		CategoryMusicLike: true, CategorySilence: true,
	}))
	mon.OnLog = func(Event) {}
	clip := realisticChunk()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.Observe(clip)
	}
}

// TestAmbientCPUBudget is the measurement P6.4 asked for and never took.
func TestAmbientCPUBudget(t *testing.T) {
	const budgetPercent = 5.0

	mon := NewChunkMonitor(NewServiceFromOptions(0.5, ResponseLog, map[Category]bool{
		CategoryLoudNoise: true, CategoryAlarmLike: true,
		CategoryMusicLike: true, CategorySilence: true,
	}))
	mon.OnLog = func(Event) {}
	clip := realisticChunk()

	res := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			mon.Observe(clip)
		}
	})
	if res.N == 0 {
		t.Skip("benchmark did not run")
	}

	perChunk := time.Duration(res.T.Nanoseconds() / int64(res.N))
	duty := float64(perChunk) / float64(budgetChunkSpan) * 100

	t.Logf("ambient analysis: %s per %s chunk = %.4f%% duty cycle (%d iterations, %d B/op)",
		perChunk.Round(time.Microsecond), budgetChunkSpan, duty, res.N,
		res.AllocedBytesPerOp())

	if duty > budgetPercent {
		t.Errorf("ambient duty cycle %.3f%% exceeds the %.1f%% budget P6.4 claims — "+
			"this runs continuously while live mode is on", duty, budgetPercent)
	}
}

// The analyzer pads its FFT to the next power of two, so a 1500ms chunk becomes a
// 32768-point transform. That is fine at these sizes and worth pinning, because
// the cost is superlinear in chunk length: doubling the wake chunk to 3s would
// cross into a 65536-point FFT and double the allocation per chunk with it.
func TestAmbientCostScalesWithChunkLength(t *testing.T) {
	mon := NewChunkMonitor(NewServiceFromOptions(0.5, ResponseLog,
		map[Category]bool{CategoryLoudNoise: true}))
	mon.OnLog = func(Event) {}

	short := speech.AudioFormat{
		Kind: speech.KindWAV, SampleRate: budgetRate, Channels: 1,
		Bytes: speech.EncodeWAVPCM16(make([]byte, budgetRate/2*2), budgetRate, 1), // 500ms
	}
	long := realisticChunk() // 1500ms

	measure := func(clip speech.AudioFormat) time.Duration {
		res := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				mon.Observe(clip)
			}
		})
		if res.N == 0 {
			return 0
		}
		return time.Duration(res.T.Nanoseconds() / int64(res.N))
	}

	shortCost, longCost := measure(short), measure(long)
	if shortCost == 0 || longCost == 0 {
		t.Skip("benchmarks did not run")
	}
	t.Logf("500ms chunk: %s · 1500ms chunk: %s",
		shortCost.Round(time.Microsecond), longCost.Round(time.Microsecond))

	// Both must stay inside the budget for their own span; that is the property
	// that matters, rather than a fixed ratio between them.
	for _, c := range []struct {
		name string
		cost time.Duration
		span time.Duration
	}{
		{"500ms", shortCost, 500 * time.Millisecond},
		{"1500ms", longCost, budgetChunkSpan},
	} {
		if duty := float64(c.cost) / float64(c.span) * 100; duty > 5.0 {
			t.Errorf("%s chunk duty cycle %.3f%% exceeds 5%%", c.name, duty)
		}
	}
}
