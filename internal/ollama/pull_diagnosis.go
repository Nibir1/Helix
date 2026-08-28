// internal/ollama/pull_diagnosis.go
// Purpose: turn a failed `ollama pull` into something the user can act on.
//
// Ollama streams the registry's own error text straight through, and the
// registry sits behind a proxy that speaks proxy. A real first run produced:
//
//	Setup failed: ollama pull error: pull model manifest: 503: upstream connect
//	error or disconnect/reset before headers. reset reason: connection timeout
//
// Every word of that is true and none of it tells the person what happened
// (Ollama's registry is having a bad minute), whose fault it is (not theirs),
// or what to do (wait, or pull by hand). It also reads like Helix broke, which
// is the part that matters: the next thing they do is file a bug or give up.
package ollama

import (
	"errors"
	"fmt"
	"strings"
)

// PullFailure classifies why a pull did not finish.
type PullFailure int

const (
	// PullRegistryDown is a transient upstream problem: 5xx, a proxy reset, or
	// a timeout reaching the registry. Retrying later is the whole fix.
	PullRegistryDown PullFailure = iota

	// PullNoSuchModel is a tag that does not exist. Retrying never helps; the
	// user needs a different name.
	PullNoSuchModel

	// PullNoDaemon is Ollama itself not running, as distinct from the registry.
	PullNoDaemon

	// PullOffline is no route to the internet at all.
	PullOffline

	// PullUnknown is everything else, reported verbatim rather than guessed at.
	PullUnknown
)

// DiagnosePull classifies a pull error and renders advice for it.
//
// Returns the classification and the lines to print, most important first.
// The raw error is always included as the last line: a diagnosis that hides
// what actually happened cannot be debugged by anyone.
func DiagnosePull(model string, err error) (PullFailure, []string) {
	if err == nil {
		return PullUnknown, nil
	}
	raw := err.Error()
	l := strings.ToLower(raw)

	switch {
	// Order matters: "connection refused" to the local daemon is a different
	// problem from a 503 out of the registry, and both contain "connect".
	case strings.Contains(l, "connection refused"),
		strings.Contains(l, "no such host") && strings.Contains(l, "127.0.0.1"):
		return PullNoDaemon, []string{
			"Ollama is not running on this machine.",
			"Start it with `ollama serve`, then re-run /setup.",
			raw,
		}

	case strings.Contains(l, "no such host"),
		strings.Contains(l, "network is unreachable"),
		strings.Contains(l, "dial tcp") && strings.Contains(l, "timeout"):
		return PullOffline, []string{
			"No route to the internet, so the model could not be downloaded.",
			"Reconnect and run /setup again, or pick a cloud provider with /provider use <name>.",
			raw,
		}

	case strings.Contains(l, "manifest") && (strings.Contains(l, "404") ||
		strings.Contains(l, "not found") || strings.Contains(l, "file does not exist")):
		return PullNoSuchModel, []string{
			fmt.Sprintf("Ollama's registry has no model called %q.", model),
			"Check the exact tag at ollama.com/library, then run /setup again.",
			raw,
		}

	case strings.Contains(l, "503"), strings.Contains(l, "502"),
		strings.Contains(l, "500"), strings.Contains(l, "upstream connect error"),
		strings.Contains(l, "connection timeout"), strings.Contains(l, "reset by peer"),
		strings.Contains(l, "eof"):
		return PullRegistryDown, []string{
			"Ollama's model registry did not answer. This is upstream of Helix and of " +
				"your machine — nothing here is misconfigured.",
			fmt.Sprintf("Try again in a few minutes, or pull it yourself: ollama pull %s", model),
			raw,
		}
	}

	return PullUnknown, []string{
		fmt.Sprintf("Could not download %q.", model),
		fmt.Sprintf("You can pull it yourself with: ollama pull %s", model),
		raw,
	}
}

// Retryable reports whether the same command could plausibly succeed later.
//
// Used to word the follow-up: "try again" is encouraging when the registry is
// down and actively misleading when the tag does not exist.
func Retryable(f PullFailure) bool {
	return f == PullRegistryDown || f == PullOffline || f == PullNoDaemon
}

// ErrPullFailed marks a pull failure so callers can decide it is not fatal
// without string-matching.
var ErrPullFailed = errors.New("ollama model pull failed")
