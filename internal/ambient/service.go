// internal/ambient/service.go
// Purpose: BlackBox Phase 6 (P6.2/P6.3) — the ambient monitor service. It
// consumes capture windows, classifies them, and applies per-category
// enable/cooldown/response-mode filtering so Helix never response-spams.
package ambient

import (
	"sort"
	"time"
)

// CategoryConfig controls one category's behavior.
type CategoryConfig struct {
	Enabled      bool
	ResponseMode ResponseMode
	Cooldown     time.Duration
}

// ServiceConfig configures the monitor. Categories is keyed by Category; a
// category absent from the map is treated as disabled.
type ServiceConfig struct {
	SampleRate int
	Categories map[Category]CategoryConfig
}

// AudioMonitorService consumes windows and emits cooldown-gated events.
type AudioMonitorService struct {
	analyzer Analyzer
	cfg      ServiceConfig
	lastFire map[Category]time.Time
	now      func() time.Time
}

// NewService builds a monitor over the given analyzer and config.
func NewService(analyzer Analyzer, cfg ServiceConfig) *AudioMonitorService {
	return &AudioMonitorService{
		analyzer: analyzer,
		cfg:      cfg,
		lastFire: make(map[Category]time.Time),
		now:      time.Now,
	}
}

// Process classifies one window and returns the events that are enabled AND
// outside their cooldown. Returned events are sorted by category for stable
// output.
func (s *AudioMonitorService) Process(samples []float64) []Event {
	now := s.now()
	var out []Event
	for _, d := range s.analyzer.Analyze(samples) {
		cc, ok := s.cfg.Categories[d.Category]
		if !ok || !cc.Enabled {
			continue
		}
		if last, ok := s.lastFire[d.Category]; ok && now.Sub(last) < cc.Cooldown {
			continue
		}
		s.lastFire[d.Category] = now
		out = append(out, Event(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// ResponseFor maps an event to its configured response mode and a default
// spoken message (P6.3). ignore mode yields an empty message.
func (s *AudioMonitorService) ResponseFor(ev Event) (ResponseMode, string) {
	cc, ok := s.cfg.Categories[ev.Category]
	if !ok {
		return ResponseIgnore, ""
	}
	switch cc.ResponseMode {
	case ResponseVocal:
		return ResponseVocal, defaultResponse(ev.Category)
	case ResponseLog:
		return ResponseLog, ""
	default:
		return ResponseIgnore, ""
	}
}

func defaultResponse(c Category) string {
	switch c {
	case CategoryLoudNoise:
		return "Are you okay?"
	case CategoryAlarmLike:
		return "I hear something that sounds like an alarm. Want me to check?"
	case CategorySilence:
		return "I lost the sound of your voice. Want me to repeat?"
	default:
		return ""
	}
}

// defaultCooldown picks a sensible per-category cooldown so Helix never
// response-spams (P6.2).
func defaultCooldown(c Category) time.Duration {
	switch c {
	case CategoryLoudNoise:
		return 10 * time.Minute
	case CategoryAlarmLike:
		return 5 * time.Minute
	case CategorySilence:
		return 2 * time.Minute
	default:
		return 15 * time.Minute
	}
}
