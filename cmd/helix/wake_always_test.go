// cmd/helix/wake_always_test.go
// Purpose: pin the rules around always-listen wake — the ones that are policy
// rather than mechanism, because the mechanism needs a microphone and these do
// not.
//
// What is deliberately NOT tested here: that a spoken word actually wakes the
// shell. That needs a recorder, a real room and a person, and §9 rule 1 keeps
// audio hardware out of the suite. It is logged as manual QA.
package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/wakeword"
)

func withWakeConfig(t *testing.T, enabled, always bool) {
	t.Helper()
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = &config.Config{}
	cfg.Speech.WakeWord.Enabled = config.BoolPtr(enabled)
	cfg.Speech.WakeWord.AlwaysListen = config.BoolPtr(always)
}

// Enabling the wake word opens the microphone at a prompt the user may sit at
// all day. ADR-005 lets voice REDUCE what is collected and never increase it,
// so switching it on must be typed — and switching it off must always work by
// voice, because a privacy control has to fail toward collecting less.
//
// The guarded FORM changed with the two switches merging: it was
// `/blackbox wake always on`, and it is `/blackbox wake on` now. That move was
// the whole risk of the merge — leaving the guard on the old wording would have
// kept the rule in the threat model and lost it in the code, so a spoken
// "blackbox wake on" could have opened the microphone at the keyboard.
func TestVoiceCannotOpenTheMicrophoneAtThePrompt(t *testing.T) {
	refused := []string{
		"/blackbox wake on",
		"/blackbox wake enable",
		"/bb wake on",
		"/BlackBox Wake On",
	}
	for _, line := range refused {
		ok, reason := voiceCommandAllowed(line)
		if ok {
			t.Errorf("voiceCommandAllowed(%q) allowed it — a spoken word must not open "+
				"the microphone at the keyboard prompt", line)
			continue
		}
		if !strings.Contains(strings.ToLower(reason), "typed") {
			t.Errorf("refusal for %q should say it has to be typed, got %q", line, reason)
		}
	}

	allowed := []string{
		"/blackbox wake off",
		"/blackbox wake disable",
		"/bb wake off",
		"/blackbox wake status",
		"/blackbox wake",
	}
	for _, line := range allowed {
		if ok, reason := voiceCommandAllowed(line); !ok {
			t.Errorf("voiceCommandAllowed(%q) refused it (%s) — voice must always be able "+
				"to stop listening", line, reason)
		}
	}
}

// The guard must not over-reach: every other /blackbox subcommand stays
// reachable by voice, including the safety valve itself.
func TestAlwaysListenGuardDoesNotBlockOtherWakeCommands(t *testing.T) {
	for _, line := range []string{
		"/blackbox wake off",
		"/blackbox wake status",
		"/blackbox status",
		"/blackbox off",
		"/blackbox on",
	} {
		if ok, reason := voiceCommandAllowed(line); !ok {
			t.Errorf("voiceCommandAllowed(%q) = false (%s), want allowed", line, reason)
		}
	}
}

// voiceStartsAlwaysListen is the predicate the guard is built on; a false
// negative here is a hole and a false positive is a lockout.
func TestVoiceStartsAlwaysListenPredicate(t *testing.T) {
	yes := []string{
		"/blackbox wake on",
		"/bb wake enable",
		"  /blackbox   wake   on  ",
	}
	no := []string{
		"/blackbox wake off",
		"/blackbox wake status",
		"/blackbox wake",
		"/blackbox log on",
		"/blackbox on",
		"wake on", // not a command at all
		"",
	}
	for _, line := range yes {
		if !voiceStartsAlwaysListen(line) {
			t.Errorf("voiceStartsAlwaysListen(%q) = false, want true", line)
		}
	}
	for _, line := range no {
		if voiceStartsAlwaysListen(line) {
			t.Errorf("voiceStartsAlwaysListen(%q) = true, want false", line)
		}
	}
}

// ON by default now (owner decision, 2026-09-09), and the guarantees that
// replace "off by default" are the ones asserted here.
//
// This test asserted the opposite and is rewritten rather than deleted: a
// default-on microphone is defensible only while these hold, so they are what a
// future change must trip over.
func TestWakeAndTheArmedPromptAreOnByDefault(t *testing.T) {
	def := config.WakeWordDefaults()
	if !def.Listening() || !def.PromptArmed() {
		t.Fatal("the wake word and the armed prompt are both on by default now")
	}

	// It arms only where it can actually work. A default that pretends is worse
	// than one that is off.
	withWakeConfig(t, true, true)
	if alwaysListenArmed() != (shell.KeyWaitSupported() && voiceEntryPreflight() == nil) {
		t.Error("arming must still require keystroke readiness, a recorder and a transcriber " +
			"— being on by default does not mean claiming to listen on a host that cannot")
	}

	// And turning it off must close BOTH halves, or "wake off" leaves a
	// microphone the user believes they just closed.
	src, err := os.ReadFile("wake.go")
	if err != nil {
		t.Fatal(err)
	}
	body := stripLineComments(functionBody(string(src), "func disableWakeWord("))
	if body == "" {
		t.Fatal("disableWakeWord not found — the test cannot reach what it checks")
	}
	if !strings.Contains(body, "AlwaysListen = config.BoolPtr(false)") {
		t.Error("disableWakeWord leaves the prompt armed — one command turned both on, so " +
			"one command has to turn both off")
	}
}

// Arming requires the wake word too. Always-listen widens WHERE the wake word
// is heard; on its own it would be a microphone held open for a detector
// nobody asked to run.
func TestArmingRequiresBothSwitches(t *testing.T) {
	withWakeConfig(t, false, true)
	if alwaysListenArmed() {
		t.Error("armed with the wake word off — always-listen extends the wake word, " +
			"it does not enable it")
	}

	withWakeConfig(t, true, false)
	if alwaysListenArmed() {
		t.Error("armed without always-listen set")
	}
}

// A platform that cannot tell us a keystroke is waiting cannot arm a prompt,
// and must say so rather than appearing to work. On Unix this asserts the
// capability is present; on Windows, that arming is refused.
func TestArmingFollowsPlatformCapability(t *testing.T) {
	withWakeConfig(t, true, true)
	if !shell.KeyWaitSupported() {
		if alwaysListenArmed() {
			t.Error("armed on a platform with no keystroke readiness — the prompt would " +
				"block and the wake word would never be heard")
		}
		if shell.ErrKeyWaitUnsupported == nil {
			t.Error("an unsupported platform must carry a reason to show the user")
		}
	}
	// The Unix expectation is covered by TestKeyReadyReportsTimeout in
	// internal/shell, which exercises the syscall itself rather than mocking it.
}

// The status line must distinguish the three states a reader cares about, and
// never report "armed" for a configuration that cannot arm.
func TestAlwaysListenStatusLineIsHonest(t *testing.T) {
	withWakeConfig(t, true, false)
	if got := shell.Plain(alwaysListenStatusLine()); !strings.Contains(got, "off") {
		t.Errorf("status with always-listen unset = %q, want it to read off", got)
	}

	withWakeConfig(t, false, true)
	got := shell.Plain(alwaysListenStatusLine())
	if strings.Contains(got, "armed") && !strings.Contains(got, "not armed") {
		t.Errorf("status = %q — must not claim armed while the wake word is off", got)
	}
}

// The wake-heard line must not name a phrase the detector cannot match.
//
// This is the fourth place in the codebase that has to resist the temptation
// (blackBoxWakeLine, wakeBannerLines and printWakeStatus are the others), and
// the reason is the same each time: the default energy engine scores loudness,
// so "heard 'hey helix'" would be a sentence Helix cannot support.
func TestWakeHeardDetailNamesNoPhraseOnTheEnergyEngine(t *testing.T) {
	withWakeConfig(t, true, true)
	cfg.Speech.WakeWord.Engine = "energy"
	cfg.Speech.WakeWord.Phrase = "hey helix"

	got := wakeHeardDetail(wakeEventForTest("hey helix", 0.42))
	if strings.Contains(strings.ToLower(got), "hey helix") {
		t.Errorf("wakeHeardDetail = %q — the energy engine cannot match a phrase, so "+
			"naming one promises what the detector does not deliver", got)
	}
	if !strings.Contains(got, "onset") {
		t.Errorf("wakeHeardDetail = %q, want it to say what actually fired", got)
	}

	// The sidecar engine IS scoring a phrase, so it may name it.
	cfg.Speech.WakeWord.Engine = "sidecar"
	got = wakeHeardDetail(wakeEventForTest("hey helix", 0.91))
	if !strings.Contains(got, "hey helix") {
		t.Errorf("sidecar wakeHeardDetail = %q, want the phrase it actually matched", got)
	}
}

// wakeEventForTest builds a wake event without a microphone.
func wakeEventForTest(phrase string, score float64) wakeword.WakeEvent {
	return wakeword.WakeEvent{
		DetectedAt: time.Now(),
		Score:      score,
		Phrase:     phrase,
	}
}

// TestWakeHoldHasNoDeadline is the regression for the defect a real session
// found on 2026-09-09: the wake gate removed itself.
//
// It read ADR-005 §5 ("a hard 60s inactivity lockout BACK TO wake-only
// listening") as a deadline ON wake-only listening, so sixty quiet seconds
// after going live the microphone opened with no gate at all. Fan noise became
// a 0.5s clip, whisper turned it into "May he leave.", and the shell answered a
// turn nobody took; two turns later a hallucinated "Manual mode." matched the
// kill phrase and ended live mode by itself.
//
// Asserted by reading the source rather than by waiting a minute, because the
// property is the ABSENCE of a timeout and a test that waited for one to not
// fire would have to run longer than the timeout it is checking for. §9 rule 8
// applies: the assertions below would both have failed against the old code.
// RETARGETED, not relaxed. The hold this used to read — the between-turns
// wake hold inside live mode — is gone, because a conversation no longer
// re-waits for a wake word before every answer. The RULE is unchanged and now
// belongs to armedIdleWait, the only wake-only hold left.
//
// The AWAKE stand-down is not a counterexample: it expires into LESS listening
// (wake-only), never into transcription. TestStandDownIsNotACaptureDeadline
// pins that it never becomes a deadline inside a capture.
func TestWakeHoldHasNoDeadline(t *testing.T) {
	src, err := os.ReadFile("wake_always.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	fn := functionBody(body, "func armedIdleWait()")
	if fn == "" {
		t.Fatal("could not find armedIdleWait — the test cannot reach what it checks")
	}
	if strings.Contains(fn, "context.WithTimeout") {
		t.Error("the wake hold has a timeout again. When it expires the caller falls " +
			"through to OPEN capture, which is ADR-005 §5 inverted: an idle session must " +
			"need the wake word MORE, not stop needing it.")
	}
	if !strings.Contains(fn, "context.WithCancel") {
		t.Error("the hold must still be cancellable — Ctrl+C is the only way to take a " +
			"turn without making a sound")
	}
	for _, f := range []string{"wake_always.go", "voice_mode.go", "awake_turn.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			continue // awake_turn.go may not exist yet
		}
		if strings.Contains(string(src), "wakeIdleWindow") {
			t.Errorf("wakeIdleWindow is back in %s; the window is the bug, not the duration", f)
		}
	}
}

// The stand-down must never become a capture deadline.
//
// It is the same defect in a new coat: a duration that ends listening, sitting
// inside the function that does the listening. Checked at the top of a turn as
// a pure function of the clock instead — see shouldStandDown — so no capture
// context ever carries it and there is no race against an in-flight recorder.
func TestStandDownIsNotACaptureDeadline(t *testing.T) {
	src, err := os.ReadFile("voice_mode.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, fn := range []string{
		"func voiceTurn(", "func batchVoiceTurn(", "func streamingVoiceTurn(",
	} {
		b := functionBody(body, fn)
		if b == "" {
			t.Fatalf("could not find %s", fn)
		}
		for _, banned := range []string{"AwakeIdleStandDown", "shouldStandDown", "awakeIdleFor"} {
			if strings.Contains(b, banned) {
				t.Errorf("%s consults %s — the stand-down has become a capture deadline, "+
					"which is the shape of the defect TestWakeHoldHasNoDeadline exists for",
					fn, banned)
			}
		}
	}
}

// A lapse in listening must be announced WITH ITS CAUSE, and a deliberate act
// must not be announced at all.
//
// This used to be asserted against a wakeOutcome table owned by the per-turn
// wake hold. That hold is gone — a conversation no longer re-waits for a wake
// word between turns — so the rule moved to the two lapses that survive.
func TestArmingLapsesNameTheirCause(t *testing.T) {
	if got := armingLapseNotice(); !strings.Contains(got, "/mictest") {
		t.Errorf("a scanner that could not start must point somewhere: %q", got)
	}

	// A reported cause always beats the generic sentence: "the wake scanner
	// stopped" sends the reader nowhere.
	withCause := armingDiedNotice(errors.New("wake capture failed 5 times in a row"))
	if !strings.Contains(withCause, "failed 5 times") {
		t.Errorf("the scanner's own reason was dropped: %q", withCause)
	}
	if generic := armingDiedNotice(nil); generic == withCause {
		t.Error("a missing cause and a reported one produced the same notice")
	}
	if !strings.Contains(armingDiedNotice(nil), "/mictest") {
		t.Error("the fallback notice points nowhere")
	}
}

// TestHelpBlackBoxStatesTheDefault renders `/help /blackbox` and reads it.
//
// This is the surface the owner actually consulted when nothing listened, and
// it said "hands-free waking between turns" — a description of the feature
// BEFORE the two switches merged, and one that answers neither "is it on?" nor
// "why is it not?". Both questions had answers the help text withheld.
//
// Rendered rather than inspected as a slice (§9 rule 12): the detail block is
// assembled from three sources and printed through a panel, and asserting on
// blackBoxDetail() alone would pass if the panel dropped it.
func TestHelpBlackBoxStatesTheDefault(t *testing.T) {
	out := shell.Plain(captureStdout(t, func() { printCommandDetail("/blackbox") }))
	if out == "" {
		t.Fatal("/help /blackbox rendered nothing — the test cannot reach what it checks")
	}
	for _, want := range []string{
		// That it is already on, in words a reader does not have to infer.
		"already listening",
		// How to use it without a command.
		"make any sound to go live",
		// The keyboard is not taken away — the owner's actual worry.
		"keep typing and nothing",
		// The upgrade case, which is the one live install that does NOT listen.
		"/blackbox wake on once",
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("/help /blackbox never says %q\n--- rendered ---\n%s", want, out)
		}
	}

	// And the one-line summary in the subcommand column must not go back to
	// describing the between-turns half as if it were the whole feature.
	var wake string
	for _, line := range blackBoxUsage {
		if strings.Contains(line, "wake on|off") {
			wake = line
		}
	}
	if wake == "" {
		t.Fatal("blackBoxUsage no longer lists the wake switch")
	}
	if !strings.Contains(wake, "default") {
		t.Errorf("the wake usage line does not say it is on by default: %q", wake)
	}
	if strings.Contains(strings.ToLower(wake), "between turns") &&
		!strings.Contains(strings.ToLower(wake), "prompt") {
		t.Errorf("the wake usage line describes only the between-turns half: %q", wake)
	}
}

// TestWakeStatusOffNamesTheConfig renders the panel a confused user reaches for.
//
// OFF can only be an explicit `"enabled": false`, because an absent key reads
// as the default and the default is on — so the panel can say WHY without
// guessing. It matters because the most common reason a reader sees this row is
// a `false` no human wrote: older builds stored the setting as a plain bool,
// which is always marshalled, so every config they saved carries one.
//
// The old text was "/blackbox wake on enables hands-free conversation", which
// answers "what do I type" and not "why is an on-by-default feature off" — the
// question the owner actually had, twice.
func TestWakeStatusOffNamesTheConfig(t *testing.T) {
	withWakeConfig(t, false, false)
	out := shell.Plain(captureStdout(t, printWakeStatus))
	if out == "" {
		t.Fatal("printWakeStatus rendered nothing")
	}
	for _, want := range []string{
		"enabled: false", // the key in their file, spelled as it appears
		"on by default",  // so they know this is a deviation, not the norm
		"/blackbox wake on",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the off state never says %q\n--- rendered ---\n%s", want, out)
		}
	}

	// And the ON state must not claim the prompt is listening: that is the row
	// below, which checks a recorder and a transcriber this one does not.
	withWakeConfig(t, true, true)
	on := shell.Plain(captureStdout(t, printWakeStatus))
	if strings.Contains(on, "enabled: false") {
		t.Errorf("the on state reports a config value that is not set:\n%s", on)
	}
	if !strings.Contains(on, "AT THE PROMPT") {
		t.Error("the panel must report where the wake word is heard, not only whether it is on")
	}
}
