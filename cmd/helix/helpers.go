package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/shell"

	"github.com/fatih/color"
)

// -------------------------------------------------------
//
//	Mock-mode basic helpers
//
// -------------------------------------------------------
func generateMockCommand(request string, env shell.Env) string {
	req := strings.ToLower(request)

	switch {
	case strings.Contains(req, "list") && strings.Contains(req, "file"):
		if env.IsUnixLike() {
			return "ls -la"
		}
		return "dir"

	case strings.Contains(req, "disk space"):
		if env.IsUnixLike() {
			return "df -h"
		}
		return "wmic logicaldisk get size,freespace,caption"

	default:
		return "echo 'Mock command for: " + request + "'"
	}
}

func generateMockResponse(question string) string {
	q := strings.ToLower(question)

	switch {
	case strings.Contains(q, "hello"):
		return "Hello! I'm Helix — your AI CLI assistant."

	case strings.Contains(q, "time"):
		return fmt.Sprintf("Current system time: %s", time.Now().Format(time.RFC1123))

	default:
		return "Mock response: " + question
	}
}

func generateMockExplanation(cmd string) string {
	return "Mock explanation for: " + cmd
}

// -------------------------------------------------------
//  AI Provider Selection
// -------------------------------------------------------

func askForAIProvider() ai.ProviderType {
	reader := bufio.NewReader(os.Stdin)

	for {
		color.Cyan("\nChoose AI mode:")
		color.Cyan("  1) OpenAI Cloud")
		color.Cyan("  2) Local Model")
		color.Cyan("Enter 1 or 2: ")

		choiceRaw, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(choiceRaw)

		switch choice {
		case "1":
			return ai.ProviderOpenAI
		case "2":
			return ai.ProviderLocal
		default:
			color.Yellow("Please type 1 or 2.")
		}
	}
}

func setupOpenAIProvider() error {
	reader := bufio.NewReader(os.Stdin)

	existing, err := ai.LoadOpenAIKeyFromDisk()
	if err == nil && strings.TrimSpace(existing) != "" {
		color.Green("Found a saved OpenAI API key.")

		color.Cyan("Use saved key or paste new?")
		color.Cyan("  1) Use saved")
		color.Cyan("  2) Paste new key")
		choiceRaw, _ := reader.ReadString('\n')

		if strings.TrimSpace(choiceRaw) == "1" {
			ai.ConfigureOpenAIKey(existing)
			return nil
		}
	}

	// Ask user for a new key
	color.Cyan("Paste your OpenAI API key:")
	keyRaw, _ := reader.ReadString('\n')
	key := strings.TrimSpace(keyRaw)

	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	if err := ai.SaveOpenAIKeyToDisk(key); err != nil {
		return err
	}

	ai.ConfigureOpenAIKey(key)
	color.Green("OpenAI API key saved.")
	return nil
}
