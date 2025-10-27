// utils.go
package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
)

// ReadLine reads a line from stdin with prompt
func ReadLine(prompt string) (string, error) {
	color.Cyan(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	return line, nil
}

// AskYesNo asks the user a yes/no question and returns true for yes
func AskYesNo(prompt string) (bool, error) {
	for {
		ans, err := ReadLine(prompt + " (yes/no): ")
		if err != nil {
			return false, err
		}
		ans = strings.ToLower(ans)
		if ans == "yes" || ans == "y" {
			return true, nil
		}
		if ans == "no" || ans == "n" {
			return false, nil
		}
		color.Yellow("Please answer 'yes' or 'no'.")
	}
}

// IsOnline performs a lightweight GET to detect internet connectivity
func IsOnline(timeout time.Duration) bool {
	client := http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	// Try multiple endpoints for reliability
	endpoints := []string{
		"https://clients3.google.com/generate_204",
		"https://connectivitycheck.gstatic.com/generate_204",
		"https://www.google.com/favicon.ico",
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(endpoint)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 204 || resp.StatusCode == 200 {
				return true
			}
		}
	}

	return false
}

// SafeTrim removes dangerous characters/newlines from AI output before executing
func SafeTrim(s string) string {
	// Basic sanitation: trim, remove trailing semicolons/newlines
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ";\n")

	// Remove multiple spaces
	space := regexp.MustCompile(`\s+`)
	s = space.ReplaceAllString(s, " ")

	return s
}

// ValidateCommand performs basic command validation
func ValidateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("empty command")
	}

	// Check for obviously malicious patterns
	maliciousPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)rm\s+-rf\s+/\s*`),
		regexp.MustCompile(`(?i)format\s+[c-z]:`),
		regexp.MustCompile(`(?i)dd\s+if=/dev/zero`),
		regexp.MustCompile(`>:\\s*/dev/sd[a-z]`),
	}

	for _, pattern := range maliciousPatterns {
		if pattern.MatchString(command) {
			return fmt.Errorf("command contains dangerous pattern")
		}
	}

	return nil
}

// ExtractPackageName extracts package name from command
func ExtractPackageName(command string) string {
	// Simple heuristic to extract package names from common commands
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:install|remove|update|search)\s+([a-zA-Z0-9._-]+)`),
		regexp.MustCompile(`(?:apt|brew|choco|winget|pacman|yum|dnf)\s+(?:install|remove|update)\s+([a-zA-Z0-9._-]+)`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(command)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

// FormatDuration formats a duration for human readability
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.String()
}

// ContainsAny checks if a string contains any of the given substrings
func ContainsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// TruncateString truncates a string to a maximum length
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// cleanAIResponse cleans and formats AI responses
func cleanAIResponse(response string) string {
	response = strings.TrimSpace(response)

	// If response is very short but not empty, accept it
	if response == "" {
		return response
	}

	// Remove common AI prefixes but be more lenient
	prefixes := []string{
		"Assistant:", "AI:", "Helix:", "Response:", "Answer:",
		"Explanation:", "The command", "This command",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(response, prefix) {
			response = strings.TrimPrefix(response, prefix)
			response = strings.TrimSpace(response)
			break // Only remove one prefix
		}
	}

	// Don't be too aggressive with short responses
	// Even a 2-word response might be valid
	if len(response) < 2 {
		return ""
	}

	return response
}

func isMostlyEnglish(text string) bool {
	// Simple heuristic: count English vs non-English characters
	if len(text) == 0 {
		return true
	}

	englishChars := 0
	totalChars := 0

	for _, char := range text {
		if char <= 127 { // ASCII range
			englishChars++
		}
		totalChars++
	}

	return float64(englishChars)/float64(totalChars) > 0.8
}
