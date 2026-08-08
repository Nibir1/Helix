// internal/utils/utils.go

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

//
// ──────────────────────────────────────────────────────────────
// BASIC I/O HELPERS
// ──────────────────────────────────────────────────────────────
//

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

//
// ──────────────────────────────────────────────────────────────
// CONNECTIVITY
// ──────────────────────────────────────────────────────────────
//

// IsOnline performs a lightweight GET to detect internet connectivity
func IsOnline(timeout time.Duration) bool {
	client := http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	endpoints := []string{
		"https://clients3.google.com/generate_204",
		"https://connectivitycheck.gstatic.com/generate_204",
		"https://www.google.com/favicon.ico",
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(endpoint)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 204 || resp.StatusCode == 200 {
				return true
			}
		}
	}

	return false
}

//
// ──────────────────────────────────────────────────────────────
// STRING SANITIZATION
// ──────────────────────────────────────────────────────────────
//

// SafeTrim removes dangerous characters/newlines from AI output before executing
func SafeTrim(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ";\n")

	space := regexp.MustCompile(`\s+`)
	s = space.ReplaceAllString(s, " ")

	return s
}

//
// ──────────────────────────────────────────────────────────────
// COMMAND SAFETY
// ──────────────────────────────────────────────────────────────
//

// ValidateCommand performs basic command validation
func ValidateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("empty command")
	}

	// Basic malicious pattern checks
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

//
// ──────────────────────────────────────────────────────────────
// PACKAGE NAME EXTRACTION
// ──────────────────────────────────────────────────────────────
//

// ExtractPackageName extracts package name from command
func ExtractPackageName(command string) string {
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

//
// ──────────────────────────────────────────────────────────────
// TIME UTILS
// ──────────────────────────────────────────────────────────────
//

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

//
// ──────────────────────────────────────────────────────────────
// STRING HELPERS
// ──────────────────────────────────────────────────────────────
//

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

// IsMostlyEnglish checks if the text is mostly English characters
func IsMostlyEnglish(text string) bool {
	if len(text) == 0 {
		return true
	}

	english := 0
	for _, char := range text {
		if char <= 127 {
			english++
		}
	}

	return float64(english)/float64(len(text)) > 0.8
}

//
// ──────────────────────────────────────────────────────────────
// QUOTE / BRACE VALIDATION
// ──────────────────────────────────────────────────────────────
//

// HasBalancedQuotes — strict validator (used in ValidateAndCleanCommand)
// FIX: Respects shell quoting rules. Double quotes inside single quotes are
// literal characters and do not toggle the double-quote state (and vice versa).
// Backslash escapes only apply outside of single quotes.
func HasBalancedQuotes(text string) bool {
	var inSingle, inDouble bool
	for i := 0; i < len(text); i++ {
		ch := text[i]

		// Backslash escapes only work outside of single quotes.
		// Inside single quotes, backslashes are literal characters.
		if ch == '\\' && !inSingle && i+1 < len(text) {
			i++ // skip the escaped character
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
	}
	return !inSingle && !inDouble
}

// HasUnbalancedQuotesQuick — light heuristic (used in ExecuteCommand)
func HasUnbalancedQuotesQuick(text string) bool {
	// Only detect obvious mistakes
	s := strings.Count(text, "'")
	d := strings.Count(text, `"`)

	// allow regex patterns like '^\.' which include escaped characters
	// allow odd number when inside pipes | grep 'regex'
	if strings.Contains(text, "grep") || strings.Contains(text, "sed") {
		return false
	}

	return s%2 != 0 || d%2 != 0
}

// DebugStringBytes prints each rune + codepoint
func DebugStringBytes(s string) {
	color.Yellow("🔍 DEBUG String bytes:")
	for i, char := range s {
		color.Yellow("  [%d] %q (U+%04X)", i, char, char)
	}
}

// FixUnmatchedQuotes intelligently repairs simple unmatched quote cases
func FixUnmatchedQuotes(cmd string) string {
	// Already safe → return
	if HasBalancedQuotes(cmd) {
		return cmd
	}

	// Missing end quote for patterns like:
	//   "*.txt
	if strings.Count(cmd, `"`) == 1 {
		return cmd + `"`
	}
	if strings.Count(cmd, `'`) == 1 {
		return cmd + `'`
	}

	// Fallback: no fix
	return cmd
}

//
// ──────────────────────────────────────────────────────────────
// FILE UTILS
// ──────────────────────────────────────────────────────────────
//

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

// ──────────────────────────────────────────────────────────────
// DEBUG HELPERS
// ──────────────────────────────────────────────────────────────
// Before running Helix (only when needed):
// export HELIX_DEBUG=1
// Disable later:
// unset HELIX_DEBUG
// This ensures we don't spam users with byte-level debug logs by default.

// DebugPrintStringBytes prints each byte/Unicode rune of a string
// including index, char, and codepoint. Only prints when DEBUG mode is enabled.
func DebugPrintStringBytes(s string) {
	// Optional: only print when HELIX_DEBUG=1
	if !IsDebugMode() {
		return
	}

	fmt.Println("DEBUG String bytes:")
	for i, r := range s {
		fmt.Printf("  [%d] '%c' (U+%04X)\n", i, r, r)
	}
}

// IsDebugMode checks if HELIX_DEBUG=1 is set
func IsDebugMode() bool {
	return strings.TrimSpace(os.Getenv("HELIX_DEBUG")) == "1"
}

// ExtractCommand cleans AI output to get just the command
func ExtractCommand(aiOutput string) string {
	// Remove all code blocks and backticks
	aiOutput = strings.ReplaceAll(aiOutput, "```bash", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```sh", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```", "")

	// Remove backticks from the entire output
	aiOutput = strings.ReplaceAll(aiOutput, "`", "")

	// Remove any markdown formatting
	aiOutput = strings.ReplaceAll(aiOutput, "**", "")

	// Take only the first non-comment, non-empty line
	lines := strings.Split(aiOutput, "\n")
	var command string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") {
			command = line
			break
		}
	}

	// Remove leading/trailing quotes
	command = strings.Trim(command, `"'`)

	// Look for typical command patterns (best-effort)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+\s+`), // Starts with command
		regexp.MustCompile(`^[a-z]+\s+`),             // Starts with lowercase word
	}

	for _, pattern := range patterns {
		if match := pattern.FindString(command); match != "" {
			command = strings.TrimSpace(command)
			break
		}
	}

	return strings.TrimSpace(command)
}
