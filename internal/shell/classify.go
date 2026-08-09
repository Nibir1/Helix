// internal/shell/classify.go
// Purpose: Unified input classification engine — the core intelligence that lets
//
//	Helix behave as a single shell accepting both shell commands and
//	natural language at one prompt, with no mode switching. This is the
//	foundation of the "Helix-as-shell" inversion.
package shell

import "strings"

// InputKind enumerates how Helix should treat one line of user input.
type InputKind int

const (
	// KindEmpty is blank or whitespace-only input.
	KindEmpty InputKind = iota
	// KindSlashCommand is a Helix control command such as /help or /git.
	KindSlashCommand
	// KindShellCommand is an executable shell command (ls, git status, ...).
	KindShellCommand
	// KindNaturalLanguage is a human request that needs the AI planner.
	KindNaturalLanguage
)

// String returns a human-readable label for the kind (debug/UX transparency).
// Args: none. Returns: label string. Complexity: O(1).
func (k InputKind) String() string {
	switch k {
	case KindEmpty:
		return "empty"
	case KindSlashCommand:
		return "slash-command"
	case KindShellCommand:
		return "shell-command"
	case KindNaturalLanguage:
		return "natural-language"
	default:
		return "unknown"
	}
}

// Classification is the result of analyzing one line of input.
type Classification struct {
	// Kind is the decided category.
	Kind InputKind
	// Confidence is 0.0–1.0; higher means safer to act on without AI review.
	Confidence float64
	// RootCommand is the detected executable when Kind == KindShellCommand.
	RootCommand string
	// Reason is a short human explanation (debug/UX transparency).
	Reason string
}

// HighConfidence is the threshold above which Helix bypasses the AI planner and
// runs a shell command directly. Below it, input falls through to the planner so
// we never mis-route an ambiguous line away from the AI.
const HighConfidence = 0.65

// shellMetachars are characters whose presence strongly implies shell syntax.
// We deliberately omit '?' because a trailing '?' usually signals a NL question.
const shellMetachars = "|&;<>()$`*"

// toSet builds an O(1) lookup set from a slice of tokens.
// Args: items. Returns: set. Complexity: O(n).
func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// shellBuiltins are POSIX/common builtins. One as the first token is a very
// strong "this is a shell command" signal.
var shellBuiltins = toSet([]string{
	"cd", "pwd", "echo", "export", "unset", "alias", "unalias", "source",
	"set", "shift", "readonly", "local", "return", "exit", "jobs", "bg",
	"fg", "disown", "history", "type", "hash", "bind", "eval", "exec",
	"getopts", "help", "logout", "printf", "test", "umask", "wait", "times",
})

// commonCommands are frequent external commands — a strong (slightly weaker than
// builtin) shell signal.
var commonCommands = toSet([]string{
	"ls", "ll", "cat", "grep", "egrep", "fgrep", "rg", "find", "locate",
	"which", "whereis", "file", "stat", "du", "df", "head", "tail", "less",
	"more", "wc", "sort", "uniq", "cut", "tr", "awk", "sed", "tee", "xargs",
	"mkdir", "rmdir", "touch", "cp", "mv", "rm", "ln", "chmod", "chown",
	"chgrp", "tar", "gzip", "gunzip", "zip", "unzip", "ssh", "scp", "rsync",
	"curl", "wget", "ping", "git", "make", "cmake", "go", "python", "python3",
	"pip", "node", "npm", "yarn", "docker", "kubectl", "helm", "terraform",
	"ansible", "vim", "nvim", "nano", "emacs", "ps", "top", "htop", "kill",
	"pkill", "date", "cal", "uptime", "whoami", "id", "sudo", "su", "man",
	"clear", "env", "printenv", "basename", "dirname", "realpath", "readlink",
	"jq", "yq", "diff", "patch", "md5sum", "sha256sum", "base64", "openssl",
	"nc", "nmap", "dig", "nslookup", "host", "ifconfig", "ip", "ss", "netstat",
	"brew", "apt", "apt-get", "pacman", "yum", "dnf", "snap", "cargo",
	"rustc", "gcc", "g++", "clang", "java", "javac", "mvn", "gradle", "ruby",
	"gem", "php", "perl", "swift", "kotlin",
})

// nlProneCommands are real commands people also use in plain English
// ("find all large files"). They require structural evidence to count as shell.
var nlProneCommands = toSet([]string{"find", "grep", "search", "locate", "look"})

// nlStartWords begin natural-language questions/requests.
var nlStartWords = toSet([]string{
	"what", "why", "how", "when", "where", "who", "which", "can", "could",
	"would", "should", "is", "are", "do", "does", "will", "show", "list",
	"tell", "explain", "help", "give", "describe",
})

// englishFunctionWords signal natural-language phrasing when several appear.
var englishFunctionWords = toSet([]string{
	"the", "a", "an", "my", "your", "me", "please", "for", "to", "of", "in",
	"on", "with", "and", "or", "into", "from", "all", "every", "any", "some",
	"this", "that", "these", "those",
})

// Classify analyzes one line of input and decides how Helix should handle it.
// It uses weighted evidence: shell structure (builtins, flags, paths, meta)
// versus natural-language cues (question marks, request words, function words).
// Args: raw input line. Returns: Classification. Complexity: O(n) in tokens.
func Classify(raw string) Classification {
	input := strings.TrimSpace(raw)
	if input == "" {
		return Classification{Kind: KindEmpty, Reason: "empty input"}
	}
	if strings.HasPrefix(input, "/") {
		return Classification{Kind: KindSlashCommand, Reason: "starts with /"}
	}

	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return Classification{Kind: KindEmpty, Reason: "no tokens"}
	}

	first := strings.ToLower(tokens[0])
	rest := tokens[1:]

	isBuiltin := shellBuiltins[first]
	isCmd := commonCommands[first]
	isNLProne := nlProneCommands[first]

	flags := countFlagTokens(rest)
	hasPath := hasPathLikeToken(tokens)
	hasMeta := containsShellMetachars(input)
	hasQuote := strings.Contains(input, "\"") || strings.Contains(input, "'")
	structural := flags > 0 || hasPath || hasMeta || hasQuote

	shellScore := 0.0
	nlScore := 0.0
	var reasons []string

	// --- Shell evidence ---
	switch {
	case isNLProne:
		// "find . -name x" (shell) vs "find all large files" (natural language).
		if structural {
			shellScore += 3.0
			reasons = append(reasons, "query-command with shell structure")
		} else {
			nlScore += 3.0
			reasons = append(reasons, "query-command spoken as plain English")
		}
	case isBuiltin:
		shellScore += 3.0
		reasons = append(reasons, "shell builtin")
	case isCmd:
		shellScore += 2.5
		reasons = append(reasons, "known command")
	}

	if hasMeta {
		shellScore += 2.0
		reasons = append(reasons, "shell metacharacters")
	}
	if flags > 0 {
		shellScore += 1.5
		reasons = append(reasons, "command flags present")
	}
	if hasPath {
		shellScore += 1.0
		reasons = append(reasons, "path/file token present")
	}
	if looksLikeAssignment(tokens[0]) {
		shellScore += 2.0
		reasons = append(reasons, "variable assignment")
	}

	// --- Natural-language evidence ---
	if strings.HasSuffix(input, "?") {
		nlScore += 3.0
		reasons = append(reasons, "ends with question mark")
	}
	if nlStartWords[strings.ToLower(firstWord(input))] {
		nlScore += 2.0
		reasons = append(reasons, "starts with a question/request word")
	}
	if englishFunctionWordCount(input) >= 2 {
		nlScore += 1.5
		reasons = append(reasons, "contains English function words")
	}
	if len(tokens) == 1 && !isCmd && !isBuiltin && !isNLProne {
		nlScore += 1.5
		reasons = append(reasons, "single unfamiliar word")
	}

	total := shellScore + nlScore
	if total == 0 {
		// No strong signals: default to the planner so the AI can still help.
		return Classification{
			Kind:       KindNaturalLanguage,
			Confidence: 0.5,
			Reason:     "no strong signals; defaulting to AI",
		}
	}

	if shellScore > nlScore {
		return Classification{
			Kind:        KindShellCommand,
			Confidence:  shellScore / total,
			RootCommand: tokens[0],
			Reason:      strings.Join(reasons, "; "),
		}
	}
	return Classification{
		Kind:       KindNaturalLanguage,
		Confidence: nlScore / total,
		Reason:     strings.Join(reasons, "; "),
	}
}

// countFlagTokens counts tokens that look like CLI flags (-x, --xyz).
// Args: tokens. Returns: count. Complexity: O(n).
func countFlagTokens(tokens []string) int {
	n := 0
	for _, t := range tokens {
		if len(t) > 1 && t[0] == '-' {
			n++
		}
	}
	return n
}

// containsShellMetachars reports whether the input contains shell operators.
// Args: s. Returns: bool. Complexity: O(n).
func containsShellMetachars(s string) bool {
	return strings.ContainsAny(s, shellMetachars)
}

// hasPathLikeToken reports whether any token looks like a file/dir path.
// Args: tokens. Returns: bool. Complexity: O(n).
func hasPathLikeToken(tokens []string) bool {
	for _, t := range tokens {
		if isPathToken(t) {
			return true
		}
	}
	return false
}

// isPathToken reports whether a single token looks like a filesystem path.
// Args: t. Returns: bool. Complexity: O(len(t)).
func isPathToken(t string) bool {
	if t == "." || t == ".." || t == "~" {
		return true
	}
	if strings.HasPrefix(t, "./") || strings.HasPrefix(t, "../") ||
		strings.HasPrefix(t, "~/") || strings.HasPrefix(t, "/") {
		return true
	}
	// A filename with a short extension (main.go, README.md).
	if strings.Contains(t, ".") && !strings.HasPrefix(t, "-") {
		return looksLikeFilename(t)
	}
	return false
}

// looksLikeFilename reports whether t resembles name.ext (not a bare number).
// Args: t. Returns: bool. Complexity: O(len(t)).
func looksLikeFilename(t string) bool {
	i := strings.LastIndex(t, ".")
	if i <= 0 || i == len(t)-1 {
		return false
	}
	ext := t[i+1:]
	if len(ext) > 6 {
		return false
	}
	name := t[:i]
	if isNumeric(name) && isNumeric(ext) {
		return false // e.g. 3.14 is a number, not a file
	}
	return true
}

// looksLikeAssignment reports whether a token is VAR=value.
// Args: token. Returns: bool. Complexity: O(len(token)).
func looksLikeAssignment(token string) bool {
	i := strings.Index(token, "=")
	if i <= 0 || i == len(token)-1 {
		return false
	}
	for _, r := range token[:i] {
		isIdent := r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isIdent {
			return false
		}
	}
	return true
}

// firstWord returns the first whitespace-delimited word, or "".
// Args: s. Returns: word. Complexity: O(n).
func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// englishFunctionWordCount counts common English function words in the input.
// Args: s. Returns: count. Complexity: O(n).
func englishFunctionWordCount(s string) int {
	n := 0
	for _, t := range strings.Fields(s) {
		cleaned := strings.ToLower(strings.Trim(t, ".,!?;:\"'"))
		if englishFunctionWords[cleaned] {
			n++
		}
	}
	return n
}

// isNumeric reports whether s is a non-empty all-digit string.
// Args: s. Returns: bool. Complexity: O(len(s)).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
