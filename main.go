package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Printf("Helix v%s — An AI Driven CLI\n", HelixVersion)

	// Load config
	cfg, err := DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	// Ensure model directory exists
	if err := cfg.EnsureModelDir(); err != nil {
		fmt.Println("Error creating model directory:", err)
		return
	}

	// Download model if not present
	if err := DownloadModel(cfg.ModelFile, ModelURL, ModelChecksum); err != nil {
		fmt.Println("⚠️  Model download error:", err)
		fmt.Println("Running in mock AI mode.")
	}

	// Start CLI loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch {
		case input == "/exit":
			fmt.Println("Exiting Helix. Goodbye!")
			return

		case strings.HasPrefix(input, "/cmd"):
			command := strings.TrimSpace(strings.TrimPrefix(input, "/cmd"))
			if command == "" {
				fmt.Println("⚠️  Please enter a command after /cmd")
				continue
			}
			// TODO: replace with real command execution (Phase 3)
			fmt.Println("Executing command:", command)

		case strings.HasPrefix(input, "/ask"):
			prompt := strings.TrimSpace(strings.TrimPrefix(input, "/ask"))
			if prompt == "" {
				fmt.Println("⚠️  Please enter a question after /ask")
				continue
			}
			// TODO: replace with real AI model call
			fmt.Println("[Helix AI] → (mock response) You asked:", prompt)

		default:
			fmt.Println("⚠️  Unknown input. Use /cmd or /ask")
		}
	}
}
