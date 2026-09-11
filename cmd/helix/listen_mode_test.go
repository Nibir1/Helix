// cmd/helix/listen_mode_test.go
// Purpose: the state machine's own guarantees — that every door reaches the
// same state, that a closed microphone cannot be reopened by voice, and that
// the inactivity stand-down is a function of the clock rather than a timer
// buried in the listening path.
package main

import (
	"testing"
	"time"

	"helix/internal/config"
)

// withListenMode sets the mode for one test and restores it after.
//
// A helper rather than direct writes, because the mode is an atomic now and a
// test that assigns it directly would compile today and race tomorrow.
func withListenMode(t *testing.T, m listenMode) {
	t.Helper()
	prev := currentMode()
	t.Cleanup(func() { modeCur.Store(int32(prev)) })
	modeCur.Store(int32(m))
}

func TestListenModeNames(t *testing.T) {
	for m, want := range map[listenMode]string{
		modeManual:  "manual",
		modeStandby: "standby",
		modeAwake:   "awake",
	} {
		if got := m.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", m, got, want)
		}
	}
}

// The ADR-005 asymmetry, asserted at the transition function rather than only
// in a command guard, so it holds for every door: voice may reduce what is
// collected but never increase it.
func TestVoiceCannotReopenAClosedMicrophone(t *testing.T) {
	withListenMode(t, modeManual)
	for _, cause := range []modeCause{causeSpoken, causeWake, causeStandDown} {
		if setListenMode(modeStandby, cause) {
			t.Errorf("cause %d reopened the microphone from manual", cause)
		}
		if currentMode() != modeManual {
			t.Fatalf("a refused transition changed the mode to %v", currentMode())
		}
	}
}

// A cause that persists must be distinguishable from one that does not, or a
// restored session would rewrite the preference it just read and a restart
// would announce a keyboard prompt it is about to replace.
func TestCausePersistenceAndAnnouncement(t *testing.T) {
	for _, tc := range []struct {
		cause             modeCause
		persist, announce bool
	}{
		{causeTyped, true, true},
		{causeSpoken, true, true},
		{causeWake, true, true},
		{causeStandDown, true, true},
		{causeRestore, false, true},
		{causeQuiesce, false, false},
	} {
		if got := tc.cause.persists(); got != tc.persist {
			t.Errorf("cause %d persists() = %v, want %v", tc.cause, got, tc.persist)
		}
		if got := tc.cause.announces(); got != tc.announce {
			t.Errorf("cause %d announces() = %v, want %v", tc.cause, got, tc.announce)
		}
	}
}

// resolveInitialMode is the ONE read of config that decides where a session
// begins. An explicit `enabled: false` — including the one older builds wrote
// as a plain bool's zero value — finally has a name.
func TestResolveInitialModeFromConfig(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })

	cfg = &config.Config{}
	if got := resolveInitialMode(); got != modeStandby {
		t.Errorf("a fresh config should start in standby, got %v", got)
	}

	cfg = &config.Config{}
	cfg.Speech.WakeWord.Enabled = config.BoolPtr(false)
	if got := resolveInitialMode(); got != modeManual {
		t.Errorf("an explicit enabled:false is manual, got %v", got)
	}

	cfg = &config.Config{}
	cfg.Speech.WakeWord.Enabled = config.BoolPtr(true)
	if got := resolveInitialMode(); got != modeStandby {
		t.Errorf("an explicit enabled:true is standby, got %v", got)
	}
}

// The stand-down is a pure function of the clock, checked before the
// microphone opens. It is deliberately NOT a context deadline in the listening
// path — that is the shape of the defect that expired a wake hold into open
// capture, and it must not come back pointing the other way.
func TestAwakeStandsDownAfterSilence(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	now := time.Now()
	noteVoiceActivity(now)

	if shouldStandDown(now.Add(9 * time.Minute)) {
		t.Error("stood down inside the window — a pause in a conversation is not the end of it")
	}
	if !shouldStandDown(now.Add(11 * time.Minute)) {
		t.Error("did not stand down after the window — the microphone would stay open")
	}

	// An explicit 0 means "never", which is a real wish and the reason the
	// field is a pointer.
	cfg.Speech.WakeWord.AwakeIdleStandDownS = config.IntPtr(0)
	if shouldStandDown(now.Add(24 * time.Hour)) {
		t.Error("stood down despite being explicitly disabled")
	}
}

// Being MISHEARD must not stand you down: presence is what resets the clock,
// not a successful transcript.
func TestVoiceActivityResetsTheStandDownClock(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	now := time.Now()
	noteVoiceActivity(now)
	if !shouldStandDown(now.Add(11 * time.Minute)) {
		t.Fatal("precondition: should have stood down")
	}
	noteVoiceActivity(now.Add(10 * time.Minute))
	if shouldStandDown(now.Add(11 * time.Minute)) {
		t.Error("a sign of presence did not reset the clock")
	}
}

// The other half of the fix for the reported loop. Without a grace window the
// tail of the sentence that asked for standby is itself a wake event on the
// energy engine, and the user is handed straight back.
func TestRearmGraceDropsAnImmediateWake(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	armRearmGrace(causeSpoken)
	if !inRearmGrace(time.Now()) {
		t.Fatal("a spoken standby did not arm the grace window")
	}
	if inRearmGrace(time.Now().Add(30 * time.Second)) {
		t.Error("the grace window never expired — standby would be deaf")
	}

	// An explicit 0 restores the old instant re-arm for anyone who wants it.
	cfg.Speech.WakeWord.RearmDelayMs = config.IntPtr(0)
	armRearmGrace(causeSpoken)
	if inRearmGrace(time.Now()) {
		t.Error("an explicitly disabled grace window still suppressed a wake")
	}
}

// A spoken exit gets a longer window than a typed one, because the phrase that
// asked for it is still in the air.
func TestSpokenStandbyGetsALongerGrace(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	armRearmGrace(causeTyped)
	typed := rearmUntil.Load()
	armRearmGrace(causeSpoken)
	spoken := rearmUntil.Load()
	if spoken <= typed {
		t.Error("a spoken standby must outlast its own echo")
	}
}
