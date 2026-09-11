// cmd/helix/nested_daemon_test.go
// Purpose: `helix daemon` at the Helix prompt, and the kill phrase's evidence
// bar. Both come from one live session.
package main

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"helix/internal/speech"
)

// The forms that cannot work are caught; the ones that can are not.
//
// A user followed Helix's own hint — "run: helix daemon" — at this prompt. The
// line reached the planner, which ran `ps aux | grep helix` instead of starting
// anything, and the same words from their shell worked first try.
func TestNestedDaemonInvocationIsNarrow(t *testing.T) {
	caught := []string{
		"helix daemon",
		"  helix daemon  ",
		"HELIX DAEMON",
		"helix daemon install",
		"helix daemon uninstall",
	}
	for _, line := range caught {
		if !nestedDaemonInvocation(line) {
			t.Errorf("nestedDaemonInvocation(%q) = false — this starts a foreground Helix "+
				"inside a Helix, both holding one terminal", line)
		}
	}

	// Allowed through: these spawn a client that talks to the socket and exits,
	// or are simply harmless. Blocking them would be a worse bug than the one
	// being fixed — it would break working commands to prevent a mistake.
	allowed := []string{
		"helix daemon status",
		"helix remote status",
		"helix remote say hello",
		"helix --version",
		"helix",
		"helixd daemon", // a different program
		"ps aux | grep helix daemon",
		"echo helix daemon",
		"",
	}
	for _, line := range allowed {
		if nestedDaemonInvocation(line) {
			t.Errorf("nestedDaemonInvocation(%q) = true — this works from the prompt and "+
				"must not be intercepted", line)
		}
	}
}

// A session-ending phrase needs more than "audible".
//
// From the live session: three turns of room noise, the last transcribed as
// "Manual mode.", and live mode ended by itself. The transcript's confidence
// was printed on the line directly above the check and ignored by it.
func TestKillPhraseNeedsEvidence(t *testing.T) {
	// WAV, because every capture path in Helix produces WAV (capture.go's
	// RecordClip and its chunk scanner both do). The first version of this test
	// built raw PCM and failed — ClipRMS decodes WAV only and reported 0 for a
	// loud clip. The code was right and the test was wrong, which is worth
	// leaving written down: a PCM clip handed to these helpers reads as silence.
	loud := speech.AudioFormat{
		Kind: speech.KindWAV, SampleRate: 16000, Channels: 1,
		Bytes: wavAtAmplitude(4000, 0.2),
	}
	whisper := speech.AudioFormat{
		Kind: speech.KindWAV, SampleRate: 16000, Channels: 1,
		Bytes: wavAtAmplitude(4000, 0.001), // below the floor, let alone 2x it
	}

	t.Cleanup(func() { modePhrasePending.active = false })

	modePhrasePending.active = false
	if !modePhraseTrusted(speech.Transcript{Provider: "whisper-local"}, loud) {
		t.Error("a clearly-spoken phrase was not trusted — the safety valve must work")
	}

	modePhrasePending.active = false
	if modePhraseTrusted(speech.Transcript{Provider: "whisper-local"}, whisper) {
		t.Error("a phrase from a near-silent clip was trusted — that is the hallucinated " +
			"\"Manual mode.\" that ended a session nobody was talking to")
	}

	// A provider that REPORTS low confidence is refused even on a loud clip:
	// it is telling us it guessed.
	modePhrasePending.active = false
	if modePhraseTrusted(speech.Transcript{Provider: "deepgram", Confidence: 0.2}, loud) {
		t.Error("a phrase the provider itself doubted was acted on")
	}

	// The confirmation round is the safety net in both directions: once Helix
	// has asked, the next hit counts however weak, so a quiet user is not
	// trapped in live mode with no keyboard.
	modePhrasePending.active = true
	if !modePhraseTrusted(speech.Transcript{Provider: "whisper-local"}, whisper) {
		t.Error("the confirmation was not honoured — a user whose mic is quiet would have " +
			"no way out but Ctrl+C")
	}
}

// Nothing to measure must not disable the valve. A streaming turn holds no
// contiguous clip, and refusing on a missing measurement would trap someone.
func TestKillPhraseTrustsWhatItCannotMeasure(t *testing.T) {
	t.Cleanup(func() { modePhrasePending.active = false })
	modePhrasePending.active = false
	if !modePhraseTrusted(speech.Transcript{Provider: "deepgram"}, speech.AudioFormat{}) {
		t.Error("a turn with no clip was refused — the streaming path never holds one, so " +
			"this would break the valve for every streaming session")
	}
}

// wavAtAmplitude builds a 16-bit mono WAV at a fixed amplitude (0..1).
func wavAtAmplitude(samples int, amp float64) []byte {
	data := make([]byte, samples*2)
	v := uint16(int16(amp * 32767))
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	out := make([]byte, 0, 44+len(data))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(data)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)     // PCM
	out = binary.LittleEndian.AppendUint16(out, 1)     // mono
	out = binary.LittleEndian.AppendUint32(out, 16000) // rate
	out = binary.LittleEndian.AppendUint32(out, 16000*2)
	out = binary.LittleEndian.AppendUint16(out, 2)
	out = binary.LittleEndian.AppendUint16(out, 16)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

// The hint must say WHERE, or it is the trap it was.
func TestWakeHintNamesTheShell(t *testing.T) {
	lines := strings.Join(wakeBannerLines("energy", "hey helix"), " ")
	if !strings.Contains(lines, "helix daemon") {
		t.Fatal("the daemon hint disappeared")
	}
	if !strings.Contains(lines, "shell") {
		t.Error("the hint names a command without naming where to run it — a user typed it " +
			"at this prompt and it reached the planner")
	}
}

// Both guards above are pinned by their PREDICATES, which is not enough.
//
// Found by mutation-testing my own tests: replacing the call sites with
// `if false` left every assertion green. A predicate nothing calls is a
// mechanism, not a behaviour (§9 rule 8), and this repo has been here before —
// deleting adoptSiblingSpeechKey's call once left its whole test file passing
// while the double key prompt came straight back. So the wiring is asserted by
// reading the source, which is the only cheap way to check that a call exists.
func TestKillPhraseGateIsWiredIntoTheTurn(t *testing.T) {
	src, err := os.ReadFile("voice_mode.go")
	if err != nil {
		t.Fatal(err)
	}
	body := stripLineComments(functionBody(string(src), "func finishVoiceTranscript("))
	if body == "" {
		t.Fatal("could not find finishVoiceTranscript — the test cannot reach what it checks")
	}

	match := strings.Index(body, "matchModePhrase(")
	gate := strings.Index(body, "modePhraseTrusted(")
	act := strings.Index(body, "setListenMode(")
	switch {
	case match < 0:
		t.Fatal("the kill phrase is no longer matched here")
	case gate < 0:
		t.Fatal("finishVoiceTranscript does not consult modePhraseTrusted — a hallucinated " +
			"\"manual mode\" from room noise ends the session again, which is the exact " +
			"failure a live session reported")
	case act < 0:
		t.Fatal("nothing acts on the phrase any more")
	case gate < match, act < gate:
		t.Errorf("order is wrong (match=%d gate=%d act=%d): the evidence check has to sit "+
			"between recognising the phrase and changing the mode", match, gate, act)
	}
}

func TestNestedDaemonCheckIsWiredIntoTheLoop(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := stripLineComments(string(src))
	if !strings.Contains(body, "nestedDaemonInvocation(ev.Text)") {
		t.Error("the REPL no longer checks for a nested `helix daemon` — the line goes back " +
			"to the planner, which answers it with `ps aux | grep helix`")
	}
	if !strings.Contains(body, "explainNestedDaemon(") {
		t.Error("nothing explains the refusal, so the input would vanish silently")
	}
}
