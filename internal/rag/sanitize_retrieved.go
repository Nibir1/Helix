// internal/rag/sanitize_retrieved.go
// Purpose: Neutralize instruction-like content in retrieved knowledge BEFORE it
// may enter a planner prompt. Strips invisible/bidi Unicode (same hazard class
// as the shell safety gate), markdown fences, backticks, JSON braces, and
// imperative injection patterns; collapses whitespace and caps length.
package rag

import (
	"regexp"
	"strings"
)

var retrievedFenceRe = regexp.MustCompile("```[a-zA-Z]*")

// retrievedImperativeRes match instruction-override / imperative patterns that
// have no legitimate place in retrieved reference data.
var retrievedImperativeRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions?|commands?|rules?)?`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)?\s*(instructions?|commands?|rules?)?`),
	regexp.MustCompile(`(?i)you\s+must\s+`),
	regexp.MustCompile(`(?i)\b(run|execute|invoke|launch)\s+` + "`?" + `(sudo|rm|curl|wget|bash|sh|chmod|chown)\b`),
	regexp.MustCompile(`(?i)\bsudo\s+(bash|sh|zsh|dash|ksh|ash|fish)\b`),
	regexp.MustCompile(`(?i)\|\s*(sudo\s+)?(bash|sh|zsh)\b`),
	regexp.MustCompile(`(?i)(system|assistant|user)\s*:`),
	regexp.MustCompile(`(?i)new\s+instructions?:`),
}

// stripInvisibleUnicode removes zero-width, bidi-control, and non-printable
// control runes — the same hazard class blocked by the shell safety gate.
//
// Args: s: input text. Returns: cleaned text. Complexity: O(len(s)).
func stripInvisibleUnicode(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '\u200B', '\u200C', '\u200D', '\uFEFF':
			continue
		case '\u202A', '\u202B', '\u202D', '\u202E', '\u202C', '\u2066', '\u2067', '\u2068', '\u2069':
			continue
		default:
			if r < 0x20 && r != '\n' && r != '\t' {
				continue
			}
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// SanitizeRetrievedText neutralizes instruction-like content in retrieved
// knowledge: invisible Unicode, markdown fences, backticks, JSON braces
// (downgraded to parens to prevent JSON injection), and imperative injection
// patterns. Whitespace is collapsed and the result capped at maxLen runes.
//
// Args:
//   - s: raw retrieved text.
//   - maxLen: maximum runes (0 = uncapped).
//
// Returns: sanitized single-line text. Complexity: O(len(s)).
func SanitizeRetrievedText(s string, maxLen int) string {
	s = stripInvisibleUnicode(s)
	s = retrievedFenceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "{", "(")
	s = strings.ReplaceAll(s, "}", ")")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	for _, re := range retrievedImperativeRes {
		s = re.ReplaceAllString(s, " [filtered] ")
	}
	s = strings.Join(strings.Fields(s), " ")
	if maxLen > 0 {
		r := []rune(s)
		if len(r) > maxLen {
			s = string(r[:maxLen]) + "…"
		}
	}
	return strings.TrimSpace(s)
}
