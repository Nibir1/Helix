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

// Enabling always-listen opens the microphone at a prompt the user may sit at
// all day. ADR-005 lets voice REDUCE what is collected and never increase it,
// so switching it on must be typed — and switching it off must always work by
// voice, because a privacy control has to fail toward collecting less.
func TestVoiceCannotOpenTheMicrophoneAtThePrompt(t *testing.T) {
	refused := []string{
		"/blackbox wake always on",
		"/blackbox wake always enable",
		"/bb wake always on",
		"/BlackBox Wake Always On",
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
		"/blackbox wake always off",
		"/blackbox wake always disable",
		"/bb wake always off",
		"/blackbox wake always status",
		"/blackbox wake always",
	}
	for _, line := range allowed {
		if ok, reason := voiceCommandAllowed(line); !ok {
			t.Errorf("voiceCommandAllowed(%q) refused it (%s) — voice must always be able "+
				"to stop listening", line, reason)
		}
	}
}

// The guard must not over-reach: the wake word's own switches, and every other
// /blackbox subcommand, stay reachable by voice.
func TestAlwaysListenGuardDoesNotBlockOtherWakeCommands(t *testing.T) {
	for _, line := range []string{
		"/blackbox wake on",
		"/blackbox wake off",
		"/blackbox wake status",
		"/blackbox status",
		"/blackbox off",
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
		"/blackbox wake always on",
		"/bb wake always enable",
		"  /blackbox   wake   always   on  ",
	}
	no := []string{
		"/blackbox wake always off",
		"/blackbox wake always",
		"/blackbox wake on",
		"/blackbox log on",
		"/blackbox on",
		"wake always on", // not a command at all
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

// Off by default, and the default is the guarantee: an unconfigured Helix does
// not hold the microphone open at its prompt.
func TestAlwaysListenIsOffByDefault(t *testing.T) {
	def := config.WakeWordDefaults()
	if def.AlwaysListen {
		t.Error("always-listen must default to off — it opens the microphone during work " +
			"that has nothing to do with voice")
	}
	if def.Enabled {
		t.Error("wake word itself must still default to off")
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
