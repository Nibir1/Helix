package metabolism

import (
	"regexp"

	"helix/internal/journal"
)

// MaxSubjectBytes bounds a step's subject (a command line, a path, a pattern).
// Requests get journal.MaxTextBytes; a subject needs less, and a pasted heredoc
// in a command line should not become the record.
const MaxSubjectBytes = 200

// secretPatterns mask credentials that commonly appear in command lines.
//
// This is a floor, not a guarantee: no pattern list catches every secret. It is
// here because recording is local-only and opt-in, and a file that only ever
// holds what the user typed deserves not to hold their API keys too. Each rule
// keeps the NAME of what was masked, so the record still says "an API key was
// exported here", which is the part a lesson could use.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// URLs with embedded credentials: https://user:pass@host
	{regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`), `${1}[redacted]@`},
	// NAME=value where NAME looks like a credential.
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY)[A-Z0-9_]*)=("[^"]*"|'[^']*'|\S+)`), `${1}=[redacted]`},
	// --password value, --token=value, and friends.
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|token|api-?key|secret|access-?key)(?:=|\s+))("[^"]*"|'[^']*'|\S+)`), `${1}[redacted]`},
	// Authorization: Bearer xyz
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), `${1}[redacted]`},
	// Well-known token shapes, wherever they appear.
	{regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|sk-ant-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,})\b`), `[redacted]`},
}

// Scrub masks credentials in s.
func Scrub(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// cleanText prepares free text (a request, an error) for disk: secrets
// masked, control characters stripped, length bounded.
func cleanText(s string) string { return journal.Redact(Scrub(s)) }

// cleanSubject is cleanText with the tighter subject bound.
func cleanSubject(s string) string {
	s = cleanText(s)
	if len(s) <= MaxSubjectBytes {
		return s
	}
	cut := MaxSubjectBytes
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
