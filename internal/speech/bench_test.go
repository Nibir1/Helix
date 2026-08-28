// internal/speech/bench_test.go
// Purpose: Phase 7 (P7.2) — benchmarks for the speech hot paths: WAV decode
// (ambient monitor + playback) and failover-chain resolution.
package speech

import (
	"path/filepath"
	"testing"

	"helix/internal/providers"
)

func BenchmarkWAVMonoDecode(b *testing.B) {
	wav := buildWAV(1, 16, 1, 8000, pcm16Bytes(make([]int16, 1024)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeWAVMono(wav); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegistrySTTChain(b *testing.B) {
	keys, err := providers.NewKeyStoreAt(filepath.Join(b.TempDir(), "secrets.json"))
	if err != nil {
		b.Fatal(err)
	}
	reg := NewRegistry(keys, providers.NewHTTPClient(5e9))
	reg.RegisterSTT(&fakeSTT{name: "primary"})
	reg.RegisterSTT(&fakeSTT{name: "backup", local: true})
	reg.SetConfig(Config{STT: STTConfig{Provider: "primary", Fallbacks: []string{"backup"}}})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.STTChain()
	}
}
