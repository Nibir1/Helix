package utils

import (
	"bufio"
	"os"
)

// HijackStdio redirects stdout and stderr to a channel.
// It returns a cleanup function to restore original stdio.
func HijackStdio(outChan chan string) func() {
	// Create a single pipe for both stdout and stderr.
	// We use one pipe to ensure logs are merged in real-time.
	r, w, _ := os.Pipe()

	// Save originals
	origStdout := os.Stdout
	origStderr := os.Stderr

	// Redirect both to the write-end of our pipe
	os.Stdout = w
	os.Stderr = w

	// Goroutine to drain the pipe and send to TUI
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			text := scanner.Text()
			// Send to TUI channel
			// These lines will now appear inside the TUI viewport
			outChan <- text
		}
	}()

	// Return cleanup function
	return func() {
		// Close the write end of the pipe to stop the scanner
		w.Close()

		// Restore original file descriptors
		os.Stdout = origStdout
		os.Stderr = origStderr
	}
}
