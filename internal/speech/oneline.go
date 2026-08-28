// internal/speech/oneline.go
//
// Purpose: a spoken utterance is one line.
//
// whisper.cpp returns one SEGMENT per line, so a single sentence spoken with a
// pause in it — or preceded by a non-speech annotation like "(coughing)" —
// comes back as text containing newlines. Nothing collapsed them, and the
// consequences ran further than they looked:
//
//   - The echo line renders as several rows with the provider label stranded
//     on the last one, which is what "❯ (coughing)\n What can you do for me?"
//     was.
//   - More seriously, that text is SUBMITTED. It reaches the classifier, the
//     planner, and the shell as input, and a newline in submitted input is not
//     a display question — it is a second line of something.
//
// So the shape is fixed once, centrally, rather than in each adapter: every
// provider in the chain returns a transcript through Registry.Transcribe, and
// a provider that starts emitting segments tomorrow gets the same treatment
// without knowing about it.
package speech

import "strings"

// OneLine collapses any run of whitespace — newlines included — to a single
// space, and trims the ends.
//
// strings.Fields does the whole job: it splits on any whitespace and discards
// empties, so "(coughing)\n  What can you do?" becomes
// "(coughing) What can you do?" with no special cases for \r\n, tabs, or the
// double spaces whisper leaves between segments.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
