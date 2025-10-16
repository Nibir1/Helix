package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
)

func main() {
	// Professional entrypoint: initialize config, environment, and history
	cfg, err := DefaultConfig()
	if err != nil {
		log.Fatalf("failed to create default config: %v", err)
	}

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(cfg.ModelPath), 0o700); err != nil {
		log.Fatalf("failed to create config dir: %v", err)
	}

	env := DetectEnvironment()

	// Load history (non-fatal)
	history, _ := LoadHistory(cfg.HistoryPath)
	_ = history // for future use (autocomplete, etc.)

	color.Cyan("Welcome to Helix — the Ultimate AI CLI\n")
	color.Green("Detected OS: %s, Shell: %s\n", env.OSName, env.Shell)

	// Check for model (stubbed)
	if _, err := os.Stat(cfg.ModelPath); os.IsNotExist(err) {
		yes, err := AskYesNo("Helix model not found. Download now?")
		if err != nil {
			log.Fatalf("failed to read input: %v", err)
		}
		if yes {
			// TODO: implement download logic in download.go
			color.Yellow("(stub) Downloading model...\n")
		} else {
			color.Yellow("Continuing without local model (stubbed runtime).\n")
		}
	}

	// Main CLI loop
	for {
		input, err := ReadLine("helix> ")
		if err != nil {
			fmt.Println("error reading input:", err)
			continue
		}
		if input == "exit" || input == "quit" {
			color.Green("Goodbye — Helix shutting down.")
			os.Exit(0)
		}

		// Very simple command parsing: /cmd or /ask
		if len(input) >= 5 && input[:5] == "/cmd " {
			user := input[5:]
			handleCmd(user, cfg, env)
			continue
		}
		if len(input) >= 5 && input[:5] == "/ask " {
			user := input[5:]
			handleAsk(user, cfg, env)
			continue
		}

		// If no prefix provided, echo help
		color.Yellow("Use '/cmd <text>' to run commands or '/ask <text>' to ask questions. Type 'exit' to quit.\n")
	}
}

func handleCmd(user string, cfg *Config, env Env) {
	color.Magenta("[user -> cmd] %s\n", user)

	// Build prompt template for the model
	prompt := fmt.Sprintf("Convert this English text into a safe shell command. OS: %s Shell: %s Text: %s", env.OSName, env.Shell, user)

	out, err := RunModel(prompt)
	if err != nil {
		color.Red("model error: %v\n", err)
		return
	}
	out = SafeTrim(out)
	color.Green("Generated command: %s\n", out)

	// Confirm execution
	ok, err := AskYesNo("Execute the above command?")
	if err != nil {
		color.Red("input error: %v\n", err)
		return
	}
	if !ok {
		color.Yellow("Command execution cancelled by user.\n")
		return
	}

	// Execute (stubbed) — for now we just append to history and simulate
	if err := AppendHistory(cfg.HistoryPath, "/cmd: "+user); err != nil {
		color.Red("failed to write history: %v\n", err)
	}

	// TODO: call ExecuteCommand(out, env) in execute.go
	color.Cyan("(stub) Executing: %s\n", out)
}

func handleAsk(user string, cfg *Config, env Env) {
	color.Magenta("[user -> ask] %s\n", user)

	online := IsOnline(2 * time.Second)
	prompt := fmt.Sprintf("You are an assistant. InternetAvailable: %t. Question: %s", online, user)

	out, err := RunModel(prompt)
	if err != nil {
		color.Red("model error: %v\n", err)
		return
	}

	color.Blue("AI: %s\n", out)

	if err := AppendHistory(cfg.HistoryPath, "/ask: "+user); err != nil {
		color.Red("failed to write history: %v\n", err)
	}
}
