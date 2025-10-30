package main

import (
	"helix/internal/ai"
	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"
	"os/exec"
	"strings"

	"github.com/fatih/color"
)

// UPDATED: Enhanced debug info to include RAG status
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

	// NEW: RAG system status
	if ragSystem != nil {
		stats := ragSystem.GetSystemStats()
		color.Cyan("RAG System: %v", stats["initialized"])
		color.Cyan("MAN Pages Indexed: %v", stats["indexed_pages"])
		if stats["initialized"].(bool) {
			color.Green("RAG: ✅ ACTIVE")
		} else {
			color.Yellow("RAG: 🔄 INDEXING")
		}
	} else {
		color.Red("RAG: ❌ NOT INITIALIZED")
	}

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

// Add this function to debug RAG issues
func debugRAGSystem() {
	color.Cyan("🔧 Debugging RAG System...")

	if ragSystem == nil {
		color.Red("❌ RAG system is nil")
		return
	}

	stats := ragSystem.GetSystemStats()
	color.Cyan("RAG Stats: %+v", stats)

	// Test MAN page access directly
	color.Blue("🧪 Testing MAN page access...")
	cmd := exec.Command("man", "ls")
	if err := cmd.Run(); err != nil {
		color.Red("❌ 'man ls' command failed: %v", err)
		color.Yellow("💡 MAN pages may not be available on this system")
	} else {
		color.Green("✅ MAN pages are accessible")
	}
}

// Debug RAG initialization issues
func debugRAGInitialization() {
	color.Red("🔧 DEBUG: RAG System State")

	// Test if MAN command works
	cmd := exec.Command("which", "man")
	output, err := cmd.Output()
	if err != nil {
		color.Red("❌ 'man' command not found on system")
	} else {
		color.Green("✅ 'man' found at: %s", strings.TrimSpace(string(output)))
	}

	// Test basic MAN page access
	cmd = exec.Command("man", "-k", "ls")
	err = cmd.Run()
	if err != nil {
		color.Red("❌ 'man -k ls' failed: %v", err)
		color.Yellow("💡 MAN database might need updating: run 'mandb'")
	} else {
		color.Green("✅ MAN pages are accessible")
	}
}
