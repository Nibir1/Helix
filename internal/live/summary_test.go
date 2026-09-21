// internal/live/summary_test.go
// Purpose: the screen-versus-voice split, asserted against the content types
// that were actually measured going wrong.
//
// Every "must stay exact" case below is a real string from the 2026-09-11
// paraphrase probe or from Helix's own output, and the sandbox-violation line
// is the one that came back with its path deleted and a fabricated explanation
// in its place. That is what this function exists to prevent, so that is what
// it is tested on.
package live

import "testing"

func TestExactContentIsNeverHandedToTheModel(t *testing.T) {
	exact := []struct{ name, reply string }{
		{"the measured fabrication", "[ERROR] sandbox violation: /tmp/helix_e2e_evil_1789033489"},
		{"a full SHA", "HEAD is now at 9f2a1c4e8b7d3056af19e2c5b0d84713a6e9f2c1"},
		{"a short git hash", "built from 8b74cf1a"},
		{"an absolute path", "wrote /Users/me/.helix/config.json"},
		{"a Windows path", `saved to C:\Users\me\helix.log`},
		{"a version string", "helix v1.5.0 is current"},
		{"a URL", "see https://example.com/docs"},
		{"more than one line", "first line\nsecond line"},
		{"a code fence", "run ```go test ./...```"},
		{"a table row", "name\tvalue"},
		{"a long reply", longReply()},
	}
	for _, c := range exact {
		t.Run(c.name, func(t *testing.T) {
			spoken, withheld := SpeakableSummary(c.reply)
			if !withheld {
				t.Fatalf("SpeakableSummary spoke it: %q", spoken)
			}
			if spoken == c.reply {
				t.Error("the exact reply was handed to the model")
			}
			if spoken == "" {
				t.Error("nothing was said at all; the user is owed a pointer to the screen")
			}
		})
	}
}

func TestConversationIsSpokenAsWritten(t *testing.T) {
	conversational := []string{
		"Done.",
		"There are three files in that directory.",
		"The name in it is Nahasat Nibir.",
		"I stopped — that would have deleted the branch.",
		"Three files deleted, 1.4 MB freed.",
	}
	for _, reply := range conversational {
		spoken, withheld := SpeakableSummary(reply)
		if withheld {
			t.Errorf("%q was withheld; a one-line answer is the case this feature exists for", reply)
		}
		if spoken != reply {
			t.Errorf("SpeakableSummary(%q) = %q, want it unchanged", reply, spoken)
		}
	}
}

// A failure must not be announced as a success. "Done — it's on screen" after a
// refusal is worse than saying nothing.
func TestWithheldFailuresAreNotAnnouncedAsSuccess(t *testing.T) {
	spoken, withheld := SpeakableSummary("[ERROR] sandbox violation: /tmp/x/y")
	if !withheld {
		t.Fatal("an error line was spoken verbatim")
	}
	if !containsFold(spoken, "didn't go through") {
		t.Errorf("a failure was summarised as %q", spoken)
	}
	spoken, _ = SpeakableSummary("Removed /tmp/build and /tmp/cache.")
	if !containsFold(spoken, "done") {
		t.Errorf("a success was summarised as %q", spoken)
	}
}

func TestEmptyReplySaysNothing(t *testing.T) {
	if spoken, withheld := SpeakableSummary("   \n  "); spoken != "" || withheld {
		t.Errorf("SpeakableSummary(blank) = %q, %v", spoken, withheld)
	}
}

func longReply() string {
	s := ""
	for len(s) <= speakableMaxRunes {
		s += "a perfectly ordinary sentence with nothing exact in it. "
	}
	return s
}

func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0
}

func indexFold(h, n string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(n) <= len(h); i++ {
		ok := true
		for j := range len(n) {
			if lower(h[i+j]) != lower(n[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
