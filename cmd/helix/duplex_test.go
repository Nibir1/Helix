// cmd/helix/duplex_test.go
// Purpose: the gpt-live-1 turn must land in the SAME pipeline as a typed line,
// and the confirmation handover must fail closed.
//
// No network and no microphone (§9 rule 1): the transport is covered by
// internal/live's loopback suite, and what is left here is routing and policy —
// which model takes this path, which funnel the turn reaches, and what happens
// to a typed confirmation when the session cannot be deafened.
package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"helix/internal/config"
	"helix/internal/speech"
)

// The model id IS the switch, and it has to be an exact match.
// gpt-live-transcribe is a DIFFERENT endpoint with a different contract
// (internal/speech/adapter_openai_realtime_stt.go), and a prefix match would
// open a WebRTC session against a transcription model.
func TestDuplexSelectedIsAnExactModelMatch(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	if cfg == nil {
		cfg = &config.Config{}
	}

	cases := []struct {
		provider, model string
		want            bool
	}{
		{"openai", "gpt-live-1", true},
		{"openai", "GPT-Live-1", true},
		{"openai", "  gpt-live-1  ", true},
		{"openai", "gpt-live-transcribe", false},
		{"openai", "gpt-live-1-mini", false},
		{"openai", "whisper-1", false},
		{"whisper-local", "gpt-live-1", false},
		{"deepgram", "gpt-live-1", false},
		{"", "", false},
	}
	for _, c := range cases {
		cfg.Speech.STT.Provider = c.provider
		cfg.Speech.STT.Model = c.model
		if got := duplexSelected(); got != c.want {
			t.Errorf("duplexSelected(%q, %q) = %v, want %v", c.provider, c.model, got, c.want)
		}
	}
}

// The whole architecture is that gpt-live-1 decides nothing. A duplex turn must
// go through finishVoiceTranscript — the funnel that holds matchModePhrase, the
// reboot phrase, the eyes-off switch, the spoken-command allowlist and the
// ChannelVoice stamp that caps risk at Medium.
//
// Asserted on the SOURCE for the reason nested_daemon_test.go gives about the
// kill-phrase gate: a predicate nothing calls is a mechanism, not a behaviour,
// and driving this path for real needs a WebRTC session and a microphone.
func TestDuplexTurnGoesThroughTheOnePipeline(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexCapture("))
	if !strings.Contains(body, "finishVoiceTranscript(") {
		t.Error("a duplex turn does not reach finishVoiceTranscript; the stop phrases, " +
			"the voice command allowlist and the Medium risk ceiling are all bypassed")
	}
	if !strings.Contains(body, "input.ChannelVoice") && !strings.Contains(body, "ChannelVoice") {
		// finishVoiceTranscript stamps the channel itself, so this is a
		// belt-and-braces read: what must NOT appear is a text stamp.
		if strings.Contains(body, "ChannelText") {
			t.Error("a duplex turn is stamped as typed input, which would give a transcript " +
				"the authority ADR-005 withholds from it")
		}
	}
}

// awakeTurn owns the keyboard, the cbreak handover and the discard-the-partial
// rule. A duplex turn must be installed as its capture hook rather than as a
// second turn loop beside it.
func TestDuplexIsInstalledAsTheAwakeCaptureHook(t *testing.T) {
	src := readSourceFile(t, "awake_turn.go")
	if !strings.Contains(src, "capture:          awakeCapture,") {
		t.Fatal("awakeHooks.capture is not awakeCapture; the duplex turn is not reachable " +
			"from AWAKE, or it has grown a loop of its own")
	}
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func awakeCapture("))
	if !strings.Contains(body, "duplexActive()") || !strings.Contains(body, "duplexCapture(") {
		t.Error("awakeCapture does not dispatch to the duplex turn")
	}
	if !strings.Contains(body, "voiceTurnWithRetry(") {
		t.Error("awakeCapture no longer falls back to the half-duplex chain")
	}
}

// An open session bills per second, so it must not outlive the conversation.
func TestTheSessionIsScopedToTheConversation(t *testing.T) {
	src := readSourceFile(t, "listen_mode.go")
	enter := stripLineComments(functionBody(src, "func enterAwakeLocked("))
	if !strings.Contains(enter, "startDuplex()") {
		t.Error("entering AWAKE does not open the duplex session")
	}
	down := stripLineComments(functionBody(src, "func tearDownConversationLocked("))
	if !strings.Contains(down, "stopDuplex()") {
		t.Error("leaving a conversation does not close the duplex session — it would keep " +
			"an open microphone and keep billing until the shell exits")
	}
}

// Two mouths saying the same reply is the first thing this got wrong.
func TestSpokenOutputIsRoutedThroughTheSessionBeforeTTS(t *testing.T) {
	for _, c := range []struct{ file, fn string }{
		{"main.go", "agentCore.OnSpeak = func(text string) {"},
		{"voice_mode.go", "func speakDirect("},
	} {
		body := stripLineComments(functionBody(readSourceFile(t, c.file), c.fn))
		if !strings.Contains(body, "duplexSpeak(") {
			t.Errorf("%s: %s does not offer the reply to the live session first; the TTS "+
				"chain would speak it as well", c.file, c.fn)
		}
	}
}

// With no session open, everything must fall back rather than swallow the reply.
func TestSpeakingWithNoSessionFallsBackToTTS(t *testing.T) {
	duplexCur.Store(nil)
	if _, ok := duplexSpeak("anything at all"); ok {
		t.Error("duplexSpeak claimed a reply with no session open; it would never be spoken")
	}
	if duplexActive() {
		t.Error("duplexActive is true with no session")
	}
}

// ADR-005 rule 2. If the session cannot be deafened, the confirmation is
// refused — never asked. A typed phrase taken while the room is still heard is
// the exact door the rule exists to keep shut.
func TestTypedConfirmationRefusesWhenTheSessionCannotBeMuted(t *testing.T) {
	asked := false
	got := askTypedUnderMute(
		func() error { return errors.New("event channel is not open") },
		func() error { t.Error("unmuted a session that was never muted"); return nil },
		func() bool { asked = true; return true },
	)
	if got {
		t.Error("the confirmation was APPROVED although the microphone stayed open")
	}
	if asked {
		t.Error("the question was asked although the room could answer it")
	}
}

// The session must come back to life whatever the answer was, and whatever the
// prompt did on its way out.
func TestTheSessionIsAlwaysUnmutedAfterATypedConfirmation(t *testing.T) {
	for _, answer := range []bool{true, false} {
		unmuted := false
		got := askTypedUnderMute(
			func() error { return nil },
			func() error { unmuted = true; return nil },
			func() bool { return answer },
		)
		if got != answer {
			t.Errorf("answer %v was reported as %v", answer, got)
		}
		if !unmuted {
			t.Errorf("answer %v left the session deaf for the rest of the conversation", answer)
		}
	}

	unmuted := false
	func() {
		defer func() { _ = recover() }()
		_ = askTypedUnderMute(
			func() error { return nil },
			func() error { unmuted = true; return nil },
			func() bool { panic("the read blew up") },
		)
	}()
	if !unmuted {
		t.Error("a panic in the prompt left the microphone muted permanently")
	}
}

// A session that cannot be reopened is degraded, not a wrong answer: the
// confirmation itself was taken under the correct conditions.
func TestAFailedUnmuteKeepsTheAnswer(t *testing.T) {
	if !askTypedUnderMute(
		func() error { return nil },
		func() error { return errors.New("channel closed") },
		func() bool { return true },
	) {
		t.Error("a failed unmute discarded a confirmation that was correctly taken")
	}
}

// /blackbox status must not grow a row on an install that never asked for this,
// and "selected" must never read as "running".
func TestStatusRowIsAbsentUnlessSelected(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	if cfg == nil {
		cfg = &config.Config{}
	}
	duplexCur.Store(nil)

	cfg.Speech.STT.Provider, cfg.Speech.STT.Model = "openai", "whisper-1"
	if line := duplexStatusLine(); line != "" {
		t.Errorf("an install that did not select full duplex shows %q", line)
	}

	cfg.Speech.STT.Provider, cfg.Speech.STT.Model = "openai", duplexModel
	line := duplexStatusLine()
	if line == "" {
		t.Fatal("a selected-but-unopened session says nothing at all")
	}
	// Whichever branch this host lands in, the MODEL leads — it is the only
	// switch there is, so nothing else answers "why is this on?".
	if !strings.Contains(line, duplexModel) {
		t.Errorf("status line %q does not name the model", line)
	}
	// The two branches diverge on whether the preflight passes, which depends
	// on a recorder, libopus and a key — none of which a test may require
	// (§9 rule 1). So each branch is asserted on the property that makes it
	// useful, and the available one's exact wording is pinned by the e2e, which
	// runs the real binary with the harness's key and RENDERS the row.
	if strings.Contains(line, "unavailable:") {
		reason := strings.TrimSpace(strings.SplitN(line, "unavailable:", 2)[1])
		if reason == "" {
			t.Error("an unavailable session says so without saying why, which is the only " +
				"actionable half of that message")
		}
	} else if !strings.Contains(line, "$0.05/min") {
		t.Errorf("status line %q does not say what an open session costs", line)
	}
}

func TestLiveConfigIsReportedOnlyWhenSet(t *testing.T) {
	if duplexLiveConfigured(config.SpeechLiveConfig{}) {
		t.Error("an empty live config reports as configured")
	}
	for name, lc := range map[string]config.SpeechLiveConfig{
		"instructions": {Instructions: "be quiet"},
		"voice":        {Voice: "cedar"},
		"create url":   {CreateURL: "https://example.invalid"},
		"session":      {Session: []byte(`{"model":"x"}`)},
		"max seconds":  {MaxSessionSeconds: 60},
	} {
		if !duplexLiveConfigured(lc) {
			t.Errorf("a live config setting %s reports as unconfigured", name)
		}
	}
}

// The escape hatches are the whole reason this wire format is survivable. A
// field that exists in config and is never read is worse than no field.
func TestEveryLiveConfigFieldReachesTheSession(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexOptions("))
	for _, field := range []string{
		"lc.Instructions", "lc.Voice", "lc.CreateURL", "lc.Session", "lc.MaxSessionSeconds",
	} {
		if !strings.Contains(body, field) {
			t.Errorf("%s is a config key nothing reads", field)
		}
	}
}

func TestDuplexSourceNeverLogsTheKey(t *testing.T) {
	for _, name := range []string{"duplex.go", "duplex_prompter.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "duplexAPIKey()") && strings.Contains(string(src), "Printf") {
			body := stripLineComments(functionBody(string(src), "func duplexAPIKey("))
			if strings.Contains(body, "Print") {
				t.Errorf("%s prints the API key", name)
			}
		}
	}
}

// An open session must never hand a reply back to the TTS chain. The microphone
// is open and being transcribed for the whole conversation, so Helix's own
// voice would be heard by the session and answered as if the user had said it.
func TestAnOpenSessionNeverFallsBackToTTS(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexSpeak("))
	// Exactly ONE unhandled return is correct: the one guarding "no session at
	// all". Every other path — no turn in flight, an empty summary, a failed
	// send — must still claim the reply, or the TTS chain speaks into a
	// microphone that is open and being transcribed.
	if n := strings.Count(body, "false"); n != 1 {
		t.Errorf("duplexSpeak returns handled=false on %d paths; only the no-session guard may", n)
	}
	if !strings.Contains(body, "if d == nil") {
		t.Error("duplexSpeak no longer distinguishes \"no session\" from \"no turn in flight\"")
	}
}

// The companion's unprompted remarks must still reach the SCREEN. voiceTurn
// drains them at the top of a turn; the duplex turn has to do the same, or they
// accumulate in a one-slot buffer and are simply overwritten.
func TestTheCompanionStillDrainsInADuplexTurn(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexCapture("))
	if !strings.Contains(body, "drainCompanion()") {
		t.Error("a duplex turn never drains the companion, so an unprompted remark is " +
			"silently discarded rather than printed")
	}
}

// The model id is the switch, so the ONLY way a user turns this on is by
// picking it in /blackbox setup — which reads the pricing catalog (ADR-006).
// A duplexModel that names nothing in the catalog is a feature with no door.
func TestTheModelIsSelectableFromTheCatalog(t *testing.T) {
	catalog, err := speech.LoadMergedCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	for _, e := range catalog {
		if e.Kind != "stt" || e.Model != duplexModel {
			continue
		}
		if e.Provider != "openai" {
			t.Errorf("%s is catalogued under %q; duplexSelected only matches openai",
				duplexModel, e.Provider)
		}
		// The two things a user cannot discover from the table itself, and both
		// of which cost them if they are missed.
		for _, warn := range []string{"libopus", "PARAPHRASE"} {
			if !strings.Contains(e.Notes, warn) {
				t.Errorf("the catalog entry never mentions %q; the setup table is where "+
					"this choice is actually made", warn)
			}
		}
		return
	}
	t.Fatalf("%s is not in the speech catalog, so /blackbox setup cannot offer it and "+
		"there is no other way to select it", duplexModel)
}

// A duplex session with no audio output is the worst failure this feature has:
// it connects, transcribes, answers and BILLS, and the user hears nothing. It
// must be caught at the door, not discovered.
func TestPreflightRefusesWithAudioOutputOff(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexPreflight("))
	if !strings.Contains(body, "audio.IsEnabled()") {
		t.Error("duplexPreflight does not check audio output; a session would open, bill, " +
			"and speak to nobody")
	}
	for _, check := range []string{"DetectRecorder", "OpusAvailable", "duplexAPIKey"} {
		if !strings.Contains(body, check) {
			t.Errorf("duplexPreflight no longer checks %s before creating a paid session", check)
		}
	}
}

// Playback is the whole output half. A failure there must be visible without
// HELIX_DEBUG.
func TestPlaybackFailureIsReportedToTheUser(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func (d *duplexSession) playModelAudio("))
	if !strings.Contains(body, "uiWarn(") {
		t.Error("a playback failure is not surfaced; the session would run silently and " +
			"look like a broken microphone")
	}
	if strings.Contains(body, "IsDebugMode") {
		t.Error("the playback failure is gated on HELIX_DEBUG, which is where it was hidden before")
	}
}

// The voice log must record what was HANDED TO THE MODEL, not the reply.
//
// They differ by design: SpeakableSummary withholds a path or a hash and sends
// "it's on screen" instead, so logging the reply would have the log claim Helix
// read a sandbox violation aloud when it deliberately did not. That log exists
// to answer "what was said"; a log that overstates it is worse than none.
func TestTheVoiceLogRecordsWhatWasActuallySent(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "main.go"),
		"agentCore.OnSpeak = func(text string) {"))
	if !strings.Contains(body, "logSpoke(spoken)") {
		t.Error("the duplex branch does not log the text that was actually sent to the model")
	}
	if strings.Contains(body, "duplexSpeak(text)") && strings.Contains(body, "logSpoke(text)\n\t\t\treturn") {
		t.Error("the duplex branch logs the full reply, overstating what was spoken")
	}
}

// The session must not outlive the conversation — an open one bills per second.
//
// VERIFIED FOR REAL on 2026-09-11 against the live service, driving the shipped
// binary over a PTY with the preset's own config: `/blackbox on` opened
// live_u0_EN1QoBbF3h4ExsBmrXjtw, `/blackbox status` showed the DUPLEX row
// badged "open" with that id, and after `/blackbox off` the same row read
// "selected". The throwaway that did it is deleted; what the suite can hold is
// the wiring that made it true, which is asserted here and in
// TestTheSessionIsScopedToTheConversation.
//
// The BADGE is what says open, never the prose — "$0.05/min while open"
// contains the word, and matching on it reported a closed session as open in
// both the e2e and the probe. Twice in one day, which is why it is written down
// in three places now.
func TestTheStatusRowDistinguishesOpenFromSelectedByBadge(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "blackbox.go"), "func blackBoxDuplexLine("))
	if !strings.Contains(body, "duplexActive()") {
		t.Error("the DUPLEX badge does not consult whether a session is actually open, " +
			"so \"selected\" and \"open\" would render identically")
	}
	// The idle wording must not itself contain the badge word, or every reader
	// of this row — human or test — has to disambiguate it.
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	if cfg == nil {
		cfg = &config.Config{}
	}
	duplexCur.Store(nil)
	cfg.Speech.STT.Provider, cfg.Speech.STT.Model = "openai", duplexModel
	if line := duplexStatusLine(); strings.Contains(blackBoxDuplexLine(line), "✔ open") {
		t.Errorf("a closed session renders the open badge: %q", line)
	}
}

// The banner named the ear, the eye and the mouth and never the brain — which
// on a duplex session is the one a user is most likely to get wrong, because
// the vendor's model hears them and answers in its own voice.
func TestTheLiveBannerNamesTheModelThatThinks(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "voice_mode.go"), "func printLiveBanner("))
	if !strings.Contains(body, "THINKING") {
		t.Error("the LIVE banner does not say which model reasons; on a duplex chain that " +
			"invites the assumption that gpt-live-1 does")
	}
	if !strings.Contains(body, "blackBoxThinkingLine()") {
		t.Error("the THINKING row is not populated from the active provider")
	}
}

// And in a duplex session the HEARING row must not describe the fallback chain
// as though it were what is listening.
func TestTheHearingRowSaysFullDuplexWhenItIs(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "blackbox.go"), "func blackBoxHearingLine("))
	if !strings.Contains(body, "duplexActive()") {
		t.Error("the HEARING row reports the provider chain in a duplex session, which " +
			"describes the fallback rather than what is actually listening")
	}
}
