// internal/ambient/monitor.go
// Purpose: Phase 6 integration seam — a ChunkMonitor decodes recorded WAV
// chunks and routes them through the analyzer, dispatching per-category
// responses; a TeeScanner wraps the wake-loop chunk source so ambient
// analysis shares the existing capture stream (roadmap P6.1).
package ambient

import (
	"context"
	"strings"

	"helix/internal/speech"
)

// ChunkSource is the minimal scanner contract satisfied by wakeword.Scanner.
type ChunkSource interface {
	NextChunk(ctx context.Context) (speech.AudioFormat, error)
	Close() error
}

// ChunkMonitor decodes recorded chunks and dispatches responses by mode.
type ChunkMonitor struct {
	svc     *AudioMonitorService
	OnSpeak func(string) // vocal responses (wired to TTS)
	OnLog   func(Event)  // log responses (wired to the journal)
}

// NewChunkMonitor builds a monitor over a service.
func NewChunkMonitor(svc *AudioMonitorService) *ChunkMonitor {
	return &ChunkMonitor{svc: svc}
}

// Observe decodes one chunk and feeds it to the analyzer. Failures (a
// non-WAV chunk, a corrupt header) are silent: ambient is best-effort and
// must never interfere with the wake loop.
func (m *ChunkMonitor) Observe(clip speech.AudioFormat) {
	if clip.Kind != speech.KindWAV {
		return
	}
	samples, err := speech.DecodeWAVMono(clip.Bytes)
	if err != nil {
		return
	}
	for _, ev := range m.svc.Process(samples) {
		mode, msg := m.svc.ResponseFor(ev)
		switch mode {
		case ResponseVocal:
			if m.OnSpeak != nil && msg != "" {
				m.OnSpeak(msg)
			}
		case ResponseLog:
			if m.OnLog != nil {
				m.OnLog(ev)
			}
		}
	}
}

// TeeScanner wraps a ChunkSource so every chunk is also observed by a monitor,
// sharing the wake-loop capture stream without changing its behavior.
type TeeScanner struct {
	inner ChunkSource
	mon   *ChunkMonitor
}

// Tee returns a scanner that tees chunks into mon.
func Tee(source ChunkSource, mon *ChunkMonitor) *TeeScanner {
	return &TeeScanner{inner: source, mon: mon}
}

func (t *TeeScanner) NextChunk(ctx context.Context) (speech.AudioFormat, error) {
	clip, err := t.inner.NextChunk(ctx)
	if err == nil {
		t.mon.Observe(clip)
	}
	return clip, err
}

func (t *TeeScanner) Close() error { return t.inner.Close() }

// ResponseModeFromString maps a config string to a ResponseMode (default log).
func ResponseModeFromString(s string) ResponseMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "vocal":
		return ResponseVocal
	case "ignore":
		return ResponseIgnore
	default:
		return ResponseLog
	}
}

// NewServiceFromOptions builds a fully-configured monitor service from flat
// options (the config package's ambient section).
func NewServiceFromOptions(sensitivity float64, mode ResponseMode, enabled map[Category]bool) *AudioMonitorService {
	if sensitivity <= 0 {
		sensitivity = 0.5
	}
	if mode == "" {
		mode = ResponseLog
	}
	cfg := ServiceConfig{Categories: make(map[Category]CategoryConfig, 4)}
	for _, c := range allCategories() {
		cfg.Categories[c] = CategoryConfig{
			Enabled:      enabled[c],
			ResponseMode: mode,
			Cooldown:     defaultCooldown(c),
		}
	}
	return NewService(Analyzer{Sensitivity: sensitivity}, cfg)
}

func allCategories() []Category {
	return []Category{CategoryLoudNoise, CategoryAlarmLike, CategoryMusicLike, CategorySilence}
}
