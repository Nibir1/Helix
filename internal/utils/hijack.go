// internal/utils/hijack.go

package utils

import (
	"bufio"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// HijackStdio redirects stdout and stderr to a Bubble Tea message channel.
// It returns a cleanup function to restore original stdio.
func HijackStdio(outChan chan tea.Msg) func() {
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
			// Send as a simple string, which satisfies the generic tea.Msg interface.
			// The TUI loop will type-assert this back to string.
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
