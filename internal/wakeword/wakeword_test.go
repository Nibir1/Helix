package wakeword

import "testing"

func TestPresetConstants(t *testing.T) {
	for _, p := range []Preset{PresetStrict, PresetBalanced, PresetLoose} {
		if p == "" {
			t.Error("preset constant must not be empty")
		}
	}
	if CooldownDefault <= 0 {
		t.Error("default cooldown must be positive")
	}
}
