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
	cfg.Speech.WakeWord.Enabled = enabled
	cfg.Speech.WakeWord.AlwaysListen = always
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
func TestAlwaysListenIsOffByDefault(t *testing.T) {
	def := config.WakeWordDefaults()
	if !def.Enabled || !def.AlwaysListen {
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
	if !strings.Contains(body, "AlwaysListen = false") {
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
func TestWakeHoldHasNoDeadline(t *testing.T) {
	src, err := os.ReadFile("voice_mode.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	fn := functionBody(body, "func wakeListenUntilArmed()")
	if fn == "" {
		t.Fatal("could not find wakeListenUntilArmed — the test cannot reach what it checks")
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
	if strings.Contains(body, "wakeIdleWindow") {
		t.Error("wakeIdleWindow is back; the window is the bug, not the duration")
	}
}

// The complement: a dead scanner must STILL fall through, or a broken
// microphone would trap the user in a hold that can never fire.
func TestDeadScannerStillFallsThrough(t *testing.T) {
	if notice := wakeLapseNotice(wakeScannerFailed); notice == "" {
		t.Error("a scanner that died must announce itself — it is the one case where wake " +
			"gating is genuinely lost, and silence there is how a user ends up talking to " +
			"a shell that cannot hear")
	}
	if notice := wakeLapseNotice(wakeInterrupted); notice != "" {
		t.Errorf("Ctrl+C is an explicit act and needs no explanation, got %q", notice)
	}
}
