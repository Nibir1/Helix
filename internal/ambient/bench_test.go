// internal/ambient/bench_test.go
// Purpose: Phase 7 (P7.2) — benchmark the analyzer hot path so the CPU budget
// (<5% idle overhead) can be evidenced.
package ambient

import (
	"math/rand"
	"testing"
)

func BenchmarkAnalyzer(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	samples := make([]float64, 1024)
	for i := range samples {
		samples[i] = 2*rng.Float64() - 1
	}
	a := Analyzer{Sensitivity: 0.5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.Analyze(samples)
	}
}
