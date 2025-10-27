package main

import (
	"helix/internal/ai"
	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"
	"strings"

	"github.com/fatih/color"
)

// Show debug information
func showDebugInfo() {
	color.Cyan("=== 🔧 HELIX DEBUG INFORMATION ===")
	color.Cyan("Version: %s", config.HelixVersion)
	color.Cyan("Model: %s", cfg.ModelFile)
	color.Cyan("OS: %s", env.OSName)
	color.Cyan("Shell: %s", env.Shell)
	color.Cyan("User: %s", env.User)
	color.Cyan("Home: %s", env.HomeDir)
	color.Cyan("Online: %v", online)
	color.Cyan("Dry Run: %v", execConfig.DryRun)
	color.Cyan("Safe Mode: %v", execConfig.SafeMode)

	// Check model status
	if ai.ModelIsLoaded() {
		color.Green("Model Status: ✅ Loaded")

		// Better model test - more specific and in English
		color.Blue("🧪 Running model test...")
		testResponse, err := ai.RunModel("Answer with one word only: Hello")
		if err != nil {
			color.Red("Model Test: ❌ Failed - %v", err)
		} else {
			cleanResponse := strings.TrimSpace(testResponse)
			color.Green("Model Test: ✅ Working - '%s'", cleanResponse)

			// Check if response is reasonable
			if len(cleanResponse) > 100 {
				color.Yellow("⚠️  Model is generating verbose responses")
			}
			if !utils.IsMostlyEnglish(cleanResponse) {
				color.Yellow("⚠️  Model is responding in non-English")
			}
		}
	} else {
		color.Red("Model Status: ❌ Not loaded")
	}

	// Check package manager
	pkgMgr := shell.DetectPackageManager(env)
	if pkgMgr.Exists {
		color.Green("Package Manager: %s", pkgMgr.Name)
	} else {
		color.Yellow("Package Manager: None detected")
	}

	// Check history
	history, _ := utils.LoadHistory(cfg.HistoryPath)
	color.Cyan("Command History: %d entries", len(history))

	color.Cyan("=================================")
}

// Show help information
func showHelp() {
	ux := ux.NewUX()
	ux.ShowHelp()
}
