package utils

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

// CleanAIResponse cleans and formats AI responses
func CleanAIResponse(response string) string {
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

// IsMostlyEnglish checks if the text is mostly English characters
func IsMostlyEnglish(text string) bool {
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

// HasBalancedQuotes checks if quotes are properly balanced - DEBUG VERSION
func HasBalancedQuotes(text string) bool {
	var inSingle, inDouble bool

	for i := 0; i < len(text); i++ {
		char := text[i]

		// Skip escaped quotes
		if char == '\\' && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\'') {
			i++
			continue
		}

		if char == '"' {
			inDouble = !inDouble
		}
		if char == '\'' {
			inSingle = !inSingle
		}
	}

	return !inSingle && !inDouble
}

// Add this debug function temporarily
func DebugStringBytes(s string) {
	color.Yellow("🔍 DEBUG String bytes:")
	for i, char := range s {
		color.Yellow("  [%d] %q (U+%04X)", i, char, char)
	}
}

// FixUnmatchedQuotes attempts to fix common quote mismatches intelligently
func FixUnmatchedQuotes(command string) string {
	// If already balanced → return immediately
	if HasBalancedQuotes(command) {
		return command
	}

	// Try to fix only well-known patterns
	if strings.Contains(command, `"*.`) && !strings.HasSuffix(command, `"`) {
		return command + `"`
	}
	if strings.Contains(command, `'*.`) && !strings.HasSuffix(command, `'`) {
		return command + `'`
	}

	// Last resort: if only one extra opening quote exists, close it
	singleQuotes := strings.Count(command, "'")
	doubleQuotes := strings.Count(command, `"`)

	if doubleQuotes%2 != 0 {
		return command + `"`
	}
	if singleQuotes%2 != 0 {
		return command + `'`
	}

	// Cannot fix confidently
	return command
}

// ReadFileSafe reads a file and returns its trimmed content; returns "" if file doesn't exist.
func ReadFileSafe(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteSecretFile writes a secret (like an API key) to a file with 0600 permissions.
func WriteSecretFile(path, value string) error {
	if err := os.WriteFile(path, []byte(strings.TrimSpace(value)+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

// BracesBalanced returns true if { and } are balanced.
func BracesBalanced(s string) bool {
	count := 0
	for _, r := range s {
		switch r {
		case '{':
			count++
		case '}':
			count--
			if count < 0 {
				return false
			}
		}
	}
	return count == 0
}

// Typewriter prints text with a smooth, natural typing animation.
// - Automatically speeds up for long text
// - Slightly slower after punctuation (more human-like)
// - Safe for multiline and large output
func Typewriter(text string) {
	runes := []rune(text)
	n := len(runes)

	// Base typing speed
	baseDelay := 14 * time.Millisecond

	// If text is long, speed it up proportionally
	if n > 300 {
		baseDelay = 6 * time.Millisecond
	} else if n > 150 {
		baseDelay = 10 * time.Millisecond
	}

	for i, c := range runes {
		fmt.Printf("%c", c)

		// Newline? small pause
		if c == '\n' {
			time.Sleep(baseDelay * 3)
			continue
		}

		// Slow down after punctuation for realism
		if strings.ContainsRune(".?!,", c) {
			time.Sleep(baseDelay * 5)
			continue
		}

		// Small natural variation (±20%)
		delay := baseDelay
		if i%7 == 0 {
			delay = baseDelay + 3*time.Millisecond
		} else if i%5 == 0 {
			delay = baseDelay - 2*time.Millisecond
		}

		time.Sleep(delay)
	}

	fmt.Println()
}
