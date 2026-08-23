// internal/journal/fuzz_test.go
// Purpose: §9 rule 5 — fuzz every new sanitizer. Redact is not a parser, but it
// is the boundary between attacker-influenced text (a transcript is whatever a
// microphone picked up, threat V1) and a file the user will later `cat`, and its
// truncation has a rune-boundary rule. `FuzzSanitizeOutput` found a real
// ordering bypass in the harness's equivalent function, which is precedent
// enough to fuzz this one.
package journal

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzRedact(f *testing.F) {
	f.Add("")
	f.Add("hello world")
	f.Add("\x1b[31mred\x1b[0m")
	f.Add("nul\x00byte")
	f.Add(strings.Repeat("é", 400))
	f.Add(strings.Repeat("😀", 200))
	f.Add(strings.Repeat("a", MaxTextBytes+10))
	f.Add("\x1b]0;title\x07")
	f.Add("mixed \x00\x1b ünïcødé 😀 " + strings.Repeat("ß", 300))

	f.Fuzz(func(t *testing.T, in string) {
		got := Redact(in)

		// 1. No control characters may survive: a transcript must never carry
		//    terminal escapes into a later read of the log.
		for _, r := range got {
			if r < 32 && r != '\t' {
				t.Fatalf("control character %q survived redaction of %q", r, in)
			}
		}

		// 2. The result must always be valid UTF-8. A severed multi-byte rune
		//    makes the enclosing JSON line unparseable, which silently DROPS the
		//    entry on read-back — the audit would lose exactly the over-long
		//    utterance someone went looking for.
		if !utf8.ValidString(got) {
			t.Fatalf("redaction produced invalid UTF-8 from %q: %q", in, got)
		}

		// 3. It must survive a marshal/unmarshal round trip unchanged, because
		//    that is what every write actually does.
		line, err := json.Marshal(VoiceEntry{Text: got})
		if err != nil {
			t.Fatalf("marshal redacted text from %q: %v", in, err)
		}
		var back VoiceEntry
		if err := json.Unmarshal(line, &back); err != nil {
			t.Fatalf("redacted text from %q did not round-trip: %v", in, err)
		}
		if back.Text != got {
			t.Fatalf("round trip changed redacted text: %q → %q", got, back.Text)
		}

		// 4. Length stays bounded. Only the ellipsis may exceed the budget, so a
		//    pasted file can never become the log.
		if len(got) > MaxTextBytes+len("…") {
			t.Fatalf("redaction of %d bytes produced %d bytes (budget %d)",
				len(in), len(got), MaxTextBytes)
		}
	})
}
