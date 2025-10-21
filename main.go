package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// package-level config so runCLI/runMockMode can access it
var cfg *Config

func main() {
	fmt.Printf("Helix v%s — An AI Driven CLI\n", HelixVersion)

	// Load configuration
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
		runMockMode()
		return
	}

	// Verify model file
	fileInfo, err := os.Stat(cfg.ModelFile)
	if err != nil {
		fmt.Printf("⚠️  Model file not found: %v\n", err)
		runMockMode()
		return
	}
	fmt.Printf("✅ Model file exists: %s (Size: %.2f MB)\n",
		cfg.ModelFile,
		float64(fileInfo.Size())/(1024*1024))

	// Load LLaMA model
	fmt.Println("🔧 Loading model...")
	if err := LoadModel(cfg.ModelFile); err != nil {
		fmt.Printf("⚠️  Failed to load model: %v\n", err)
		fmt.Println("This could indicate:")
		fmt.Println("  - Corrupted model file")
		fmt.Println("  - Incompatible model format")
		fmt.Println("  - Insufficient RAM/VRAM")
		fmt.Println("  - Model requires different llama.cpp version")

		// Try to test with a simple prediction
		fmt.Println("\n🧪 Attempting test prediction...")
		if testErr := testModelPrediction(); testErr != nil {
			fmt.Printf("❌ Test prediction failed: %v\n", testErr)
		}

		fmt.Println("\nRunning in mock AI mode.")
		runMockMode()
		return
	}

	defer CloseModel()
	fmt.Println("✅ Model loaded successfully!")

	// Test the model with a simple prediction
	fmt.Println("🧪 Testing model with simple prompt...")
	testResponse, err := RunModel("Say 'Hello' in one word:")
	if err != nil {
		fmt.Printf("⚠️  Model test failed: %v\n", err)
		fmt.Println("Running in mock AI mode.")
		runMockMode()
		return
	}

	fmt.Printf("✅ Model test response: %s\n", testResponse)
	fmt.Println("🎉 Helix is ready to use!")

	// Start CLI loop
	runCLI()
}

func testModelPrediction() error {
	// Try to create a simple prediction to verify model works
	testPrompt := "Hello"
	response, err := RunModel(testPrompt)
	if err != nil {
		return fmt.Errorf("test prediction failed: %v", err)
	}
	fmt.Printf("✅ Test prediction successful: %s\n", response)
	return nil
}

func runMockMode() {
	fmt.Println("\n🔧 MOCK MODE ACTIVATED")
	fmt.Println("Commands will be simulated without AI processing")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("[helix-mock]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch {
		case input == "/exit":
			fmt.Println("Exiting Helix. Goodbye!")
			return
		case input == "/debug":
			fmt.Println("=== DEBUG INFO ===")
			fmt.Printf("Model path: %s\n", cfg.ModelFile)
			if _, err := os.Stat(cfg.ModelFile); err == nil {
				fmt.Println("✅ Model file exists")
			} else {
				fmt.Println("❌ Model file missing")
			}
		case strings.HasPrefix(input, "/cmd"):
			command := strings.TrimSpace(strings.TrimPrefix(input, "/cmd"))
			fmt.Printf("📝 [MOCK] Would execute: %s\n", command)
		case strings.HasPrefix(input, "/ask"):
			prompt := strings.TrimSpace(strings.TrimPrefix(input, "/ask"))
			fmt.Printf("🤖 [MOCK AI] Thinking about: %s\n", prompt)
			fmt.Println("🤖 [MOCK AI] → This is a simulated response since the AI model failed to load.")
		default:
			fmt.Println("❓ Available commands: /ask, /cmd, /debug, /exit")
		}
	}
}

func runCLI() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\n💫 Helix AI is ready! Type /ask followed by your question.")

	for {
		fmt.Print("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch {
		case input == "/exit":
			fmt.Println("Exiting Helix. Goodbye!")
			return
		case input == "/debug":
			fmt.Println("=== DEBUG INFO ===")
			fmt.Printf("Model: %s\n", cfg.ModelFile)
			fmt.Println("Status: ✅ Model loaded and working")
		case strings.HasPrefix(input, "/cmd"):
			command := strings.TrimSpace(strings.TrimPrefix(input, "/cmd"))
			fmt.Printf("Executing: %s\n", command)
			// TODO: implement actual command execution
		case strings.HasPrefix(input, "/ask"):
			prompt := strings.TrimSpace(strings.TrimPrefix(input, "/ask"))
			if prompt == "" {
				fmt.Println("⚠️  Please enter a question after /ask")
				continue
			}

			fmt.Printf("🤖 Processing: %s\n", prompt)
			response, err := RunModel(prompt)
			if err != nil {
				fmt.Printf("⚠️  AI error: %v\n", err)
			} else {
				fmt.Printf("🤖 [Helix AI] → %s\n", response)
			}
		default:
			fmt.Println("❓ Available commands:")
			fmt.Println("   /ask <question> - Ask the AI a question")
			fmt.Println("   /cmd <command>  - Execute a system command")
			fmt.Println("   /debug          - Show debug information")
			fmt.Println("   /exit           - Exit Helix")
		}
	}
}
