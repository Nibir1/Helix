package main

import (
	"testing"

	"helix/internal/ai"
	"helix/internal/config"
)

func TestZZRenderProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var err error
	cfg, err = config.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	_ = ai.InitProviders(ai.ProviderSettings{Provider: "deepseek"})
	displayProviderStatus()
	uiToggle("AUDIO", true, "tonal feedback plays", "no tonal feedback", "/audio <on|off>")
	uiToggle("TYPEWRITE-ALL", false, "every line is animated", "only AI replies are animated", "/typewrite-all <on|off>")
}
