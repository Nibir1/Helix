package main

import (
	"os"
	"runtime"
	"strings"
)

// Env contains detected environment info.
type Env struct {
	OSName string // windows, linux, darwin
	Shell  string // bash, zsh, powershell, cmd, unknown
}

// DetectEnvironment inspects OS and environment variables to determine the user's shell and OS.
func DetectEnvironment() Env {
	osName := runtime.GOOS
	shell := detectShellFromEnv()

	// On Windows, `SHELL` is not typically set; use heuristics
	if osName == "windows" {
		// PowerShell sets the PSModulePath env var in many environments
		if _, ok := os.LookupEnv("PSModulePath"); ok {
			shell = "powershell"
		} else if comspec := os.Getenv("ComSpec"); strings.Contains(strings.ToLower(comspec), "cmd") {
			shell = "cmd"
		}
	}

	return Env{OSName: osName, Shell: shell}
}

func detectShellFromEnv() string {
	// Typical shells on *nix: /bin/bash, /bin/zsh, etc.
	sh := os.Getenv("SHELL")
	if sh == "" {
		return "unknown"
	}
	sh = strings.ToLower(sh)
	switch {
	case strings.Contains(sh, "bash"):
		return "bash"
	case strings.Contains(sh, "zsh"):
		return "zsh"
	case strings.Contains(sh, "fish"):
		return "fish"
	default:
		return "unknown"
	}
}
