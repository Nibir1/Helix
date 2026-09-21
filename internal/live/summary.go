// internal/live/summary.go
// Purpose: decide, per reply, what gpt-live-1 is allowed to say.
//
// THE MEASUREMENT THIS EXISTS FOR. session.commentary.append is documented as
// paraphrased, and on 2026-09-11 it was measured as worse than that:
//
//	sent   [ERROR] sandbox violation: /tmp/helix_e2e_evil_1789033489
//	spoken "Sandbox violation. That action touched a forbidden path."
//
// The path was deleted and the explanation was INVENTED — a fluent sentence
// about a forbidden path that Helix never wrote. A name sent on its own was
// spoken as "On it." and a second attempt as "Nah.". Conversational prose
// ("Done — it's on screen.") survives essentially intact; exact content does
// not survive at all reliably.
//
// So the rule is not "speak less". It is per-content-type, which is what the
// brief asked for and what the measurements support:
//
//	THE SCREEN CARRIES EXACT OUTPUT      printed, unparaphrased, as it is today
//	GPT-LIVE-1 CARRIES THE CONVERSATION  acknowledgements, questions, one-liners
//
// A one-line answer really is nicer spoken, and refusing to speak it would make
// the feature worse than the half-duplex chain it replaces. A stack trace, a
// SHA or a sandbox violation is not a one-line answer.
package live

import (
	"regexp"
	"strings"
)

// speakableMaxRunes bounds a reply that may be spoken whole.
//
// 240 is about two spoken sentences. Past that the reply is a screenful, the
// listener has stopped tracking it, and the paraphrase has more room to drift —
// every measured failure was longer or more structured than this.
const speakableMaxRunes = 240

var (
	// hexRunRe matches a SHA-shaped token. Seven is git's short-hash length,
	// and a seven-hex-digit word is not English.
	hexRunRe = regexp.MustCompile(`\b[0-9a-fA-F]{7,}\b`)

	// absPathRe matches a POSIX or Windows absolute path.
	absPathRe = regexp.MustCompile(`(^|\s)(~?/[^\s]*/|[A-Za-z]:\\)`)

	// levelTagRe matches a bracketed severity marker — [ERROR], [warn], [FATAL].
	levelTagRe = regexp.MustCompile(`\[[A-Za-z]{3,8}\]`)

	// versionRe matches a version string.
	versionRe = regexp.MustCompile(`\bv?\d+\.\d+\.\d+`)

	// urlRe matches an http(s) URL.
	urlRe = regexp.MustCompile(`https?://`)

	// errorWordRe spots a reply that reads as a failure, which changes the
	// referral wording — "done, it's on screen" is wrong for a refusal.
	errorWordRe = regexp.MustCompile(`(?i)\b(error|failed|failure|denied|violation|refused|cannot|blocked)\b`)
)

// SpeakableSummary returns what to send to Commentary for a reply, and whether
// the reply was withheld because it carries exact content.
//
// When exact is true the caller must still PRINT the reply in full — this
// function decides what is spoken, never what is shown. Nothing here is a
// security control: the model has no tools in client delegation and cannot act
// on anything. It is a correctness control, and the thing it prevents is Helix
// telling a user something that is not true.
func SpeakableSummary(reply string) (spoken string, exact bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return "", false
	}
	if !MustStayExact(trimmed) {
		return trimmed, false
	}
	if errorWordRe.MatchString(trimmed) {
		return "That didn't go through — the details are on screen.", true
	}
	return "Done — it's on screen.", true
}

// MustStayExact reports whether a reply contains content the model must not be
// trusted to repeat.
//
// Deliberately over-eager. A false positive costs one spoken sentence replaced
// by "it's on screen", which is mildly less pleasant; a false negative costs a
// fabricated fact said aloud in a confident voice, which is the failure this
// whole package has to avoid. The asymmetry is the same one ADR-005's
// eyes-off phrase is matched loosely for.
func MustStayExact(reply string) bool {
	if strings.Contains(reply, "\n") {
		return true
	}
	if len([]rune(reply)) > speakableMaxRunes {
		return true
	}
	if strings.Contains(reply, "```") || strings.Contains(reply, "\t") {
		return true
	}
	return hexRunRe.MatchString(reply) ||
		absPathRe.MatchString(reply) ||
		levelTagRe.MatchString(reply) ||
		versionRe.MatchString(reply) ||
		urlRe.MatchString(reply)
}
