// internal/ambient/service_test.go
// Purpose: Phase 6 (P6.2/P6.3) — cooldown suppression, disabled-category
// filtering, and response-mode mapping, driven by a controllable clock.
package ambient

import (
	"testing"
	"time"
)

func TestServiceCooldownSuppresses(t *testing.T) {
	svc := NewService(Analyzer{Sensitivity: 0.5}, ServiceConfig{
		Categories: map[Category]CategoryConfig{
			CategoryAlarmLike: {Enabled: true, ResponseMode: ResponseLog, Cooldown: 10 * time.Minute},
		},
	})
	// Deterministic clock we can advance.
	now := time.Unix(0, 0)
	svc.now = func() time.Time { return now }

	tone := sine(50, 0.8)

	if got := svc.Process(tone); len(got) != 1 {
		t.Fatalf("first alarm must fire, got %+v", got)
	}
	now = now.Add(5 * time.Minute)
	if got := svc.Process(tone); len(got) != 0 {
		t.Fatalf("alarm within cooldown must be suppressed, got %+v", got)
	}
	now = now.Add(6 * time.Minute)
	if got := svc.Process(tone); len(got) != 1 {
		t.Fatalf("alarm after cooldown must fire again, got %+v", got)
	}
}

func TestServiceDisabledCategory(t *testing.T) {
	svc := NewService(Analyzer{Sensitivity: 0.5}, ServiceConfig{
		Categories: map[Category]CategoryConfig{
			// loud_noise enabled, but the incoming event is alarm_like → absent → disabled.
			CategoryLoudNoise: {Enabled: true, ResponseMode: ResponseLog, Cooldown: time.Minute},
		},
	})
	if got := svc.Process(sine(50, 0.8)); len(got) != 0 {
		t.Fatalf("unconfigured category must be treated as disabled, got %+v", got)
	}
}

func TestServiceResponseMapping(t *testing.T) {
	svc := NewService(Analyzer{Sensitivity: 0.5}, ServiceConfig{
		Categories: map[Category]CategoryConfig{
			CategoryLoudNoise: {Enabled: true, ResponseMode: ResponseVocal, Cooldown: time.Minute},
			CategoryAlarmLike: {Enabled: true, ResponseMode: ResponseLog, Cooldown: time.Minute},
			CategoryMusicLike: {Enabled: true, ResponseMode: ResponseIgnore, Cooldown: time.Minute},
		},
	})

	if mode, msg := svc.ResponseFor(Event{Category: CategoryLoudNoise}); mode != ResponseVocal || msg == "" {
		t.Fatalf("loud_noise must map to a vocal message, got mode=%v msg=%q", mode, msg)
	}
	if mode, msg := svc.ResponseFor(Event{Category: CategoryAlarmLike}); mode != ResponseLog || msg != "" {
		t.Fatalf("alarm_like must map to log-only, got mode=%v msg=%q", mode, msg)
	}
	if mode, _ := svc.ResponseFor(Event{Category: CategoryMusicLike}); mode != ResponseIgnore {
		t.Fatalf("music_like must map to ignore, got mode=%v", mode)
	}
}
