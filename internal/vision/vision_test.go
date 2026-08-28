package vision

import "testing"

func TestConfigDefaultsContract(t *testing.T) {
	// Enabled must default to false — opt-in is a privacy guarantee.
	var c Config
	if c.Enabled {
		t.Error("vision must be disabled by default")
	}
}
