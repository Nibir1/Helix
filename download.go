package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/schollz/progressbar/v3"
)

// DownloadModel checks if the model exists; if not, it asks the user for permission,
// downloads it with a progress bar, and verifies integrity.
func DownloadModel(modelPath, url, expectedChecksum string) error {
	// Ensure model directory exists
	if err := os.MkdirAll(filepath.Dir(modelPath), 0755); err != nil {
		return fmt.Errorf("failed to create model directory: %w", err)
	}

	// Skip download if already present
	if _, err := os.Stat(modelPath); err == nil {
		fmt.Println("✅ Model already exists locally.")
		return nil
	}

	var consent string
	fmt.Print("Helix model not found. Download now? (yes/no): ")
	fmt.Scanln(&consent)
	if consent != "yes" {
		fmt.Println("Skipping model download. Helix will run in mock AI mode.")
		return nil
	}

	// Start download
	fmt.Println("⬇️  Downloading model from:", url)
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad response: %s", resp.Status)
	}

	out, err := os.Create(modelPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	bar := progressbar.NewOptions64(
		resp.ContentLength,
		progressbar.OptionSetDescription("Downloading..."),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(40),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)

	hasher := sha256.New()
	writer := io.MultiWriter(out, hasher, bar)
	if _, err = io.Copy(writer, resp.Body); err != nil {
		return fmt.Errorf("failed while downloading: %w", err)
	}

	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	fmt.Println("\nVerifying model integrity...")
	if expectedChecksum != "" && actualChecksum != expectedChecksum {
		os.Remove(modelPath)
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	fmt.Println("✅ Model downloaded and verified successfully!")
	return nil
}
