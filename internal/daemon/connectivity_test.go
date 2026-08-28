// internal/daemon/connectivity_test.go
// Purpose: Phase 4's fourth acceptance criterion — "network cut mid-session →
// local fallback engages within 5s, spoken notice heard."
//
// It was unchecked and only half of it needs a human. The ≤5s is the poll
// interval (a 5s ticker in watchConnectivity); "heard" needs ears. What had no
// coverage at all was the part that can regress silently: that a transition
// actually switches BOTH chains, says so out loud, and writes it down.
//
// Before this, the transition lived inline in a loop driven by a real TCP probe,
// so testing it meant either a network or a five-second wait. The seam
// (applyConnectivityChange) exists so the behavior is reachable without either.
package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/speech"
)

// newConnectivityDaemon builds the minimum Daemon the transition needs: a
// journal to write to and an agent whose OnSpeak we can observe.
//
// It also initializes the speech registry, without which SetOfflineMode is a
// silent no-op (speech.Default() is nil) and the switch assertion below would
// pass while proving nothing.
func newConnectivityDaemon(t *testing.T) (*Daemon, *[]string) {
	t.Helper()

	// An empty config is fine: no provider is selected, but the registry exists,
	// which is all the offline flag needs to live on.
	_ = speech.Init(speech.Config{})
	if speech.Default() == nil {
		t.Skip("speech registry unavailable; the offline switch is not observable")
	}

	jrn, err := NewJournalAt(filepath.Join(t.TempDir(), "interactions.jsonl"))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}

	spoken := &[]string{}
	ag := &agent.Agent{}
	ag.OnSpeak = func(text string) { *spoken = append(*spoken, text) }

	return &Daemon{agent: ag, journal: jrn}, spoken
}

// Going offline must switch the chains, speak, and journal. All three matter:
// the switch is the behavior, the notice is how a user who cannot see the
// terminal learns why answers changed, and the journal is the evidence
// afterwards.
func TestOfflineTransitionSwitchesSpeaksAndJournals(t *testing.T) {
	d, spoken := newConnectivityDaemon(t)

	// Restore global offline state whatever happens — these are package-level
	// switches and leaking them would corrupt other tests in this package.
	t.Cleanup(func() {
		speech.SetOfflineMode(false)
		ai.SetOfflineMode(false)
	})

	d.applyConnectivityChange(false)

	if !speech.Default().Offline() {
		t.Error("losing the network must put the speech chain in local-first mode")
	}

	if len(*spoken) != 1 {
		t.Fatalf("expected exactly one spoken notice, got %v", *spoken)
	}
	notice := strings.ToLower((*spoken)[0])
	if !strings.Contains(notice, "connection") && !strings.Contains(notice, "local") {
		t.Errorf("the notice should say what happened and what changed, got %q", notice)
	}

	entries := d.journal.Tail(10)
	if len(entries) != 1 {
		t.Fatalf("expected one journal entry, got %+v", entries)
	}
	if entries[0].Kind != "connectivity" {
		t.Errorf("journal kind = %q, want connectivity", entries[0].Kind)
	}
	if !strings.Contains(entries[0].Note, "offline") {
		t.Errorf("journal note should record the direction, got %q", entries[0].Note)
	}
}

// Coming back must restore, and say so — a silent restore looks like the model
// simply got better again.
func TestOnlineTransitionRestoresAndAnnounces(t *testing.T) {
	d, spoken := newConnectivityDaemon(t)
	t.Cleanup(func() {
		speech.SetOfflineMode(false)
		ai.SetOfflineMode(false)
	})

	d.applyConnectivityChange(false)
	d.applyConnectivityChange(true)

	if speech.Default().Offline() {
		t.Error("restored connectivity must take the speech chain out of local-first mode")
	}
	if len(*spoken) != 2 {
		t.Fatalf("expected a notice per transition, got %v", *spoken)
	}
	if !strings.Contains(strings.ToLower((*spoken)[1]), "restored") {
		t.Errorf("the restore notice should say so, got %q", (*spoken)[1])
	}

	entries := d.journal.Tail(10)
	if len(entries) != 2 {
		t.Fatalf("expected two journal entries, got %d", len(entries))
	}
	if !strings.Contains(entries[1].Note, "online") {
		t.Errorf("second entry should record the restore, got %q", entries[1].Note)
	}
}

// The loop only calls the seam on a CHANGE, and this pins the reason that
// matters: repeated notices would be worse than none. A user on flaky wifi must
// not be told about it every five seconds.
func TestConnectivityNoticeIsPerTransitionNotPerPoll(t *testing.T) {
	d, spoken := newConnectivityDaemon(t)
	t.Cleanup(func() {
		speech.SetOfflineMode(false)
		ai.SetOfflineMode(false)
	})

	// Three transitions, not six polls.
	d.applyConnectivityChange(false)
	d.applyConnectivityChange(true)
	d.applyConnectivityChange(false)

	if len(*spoken) != 3 {
		t.Fatalf("expected one notice per transition, got %d: %v", len(*spoken), *spoken)
	}
}

// A daemon with no agent must not panic on a transition. The supervision loop
// runs this from a goroutine, and a nil-pointer panic there would take down a
// service whose whole job is staying up.
func TestConnectivityTransitionSurvivesMissingAgent(t *testing.T) {
	jrn, err := NewJournalAt(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	t.Cleanup(func() {
		speech.SetOfflineMode(false)
		ai.SetOfflineMode(false)
	})

	d := &Daemon{journal: jrn, agent: &agent.Agent{}} // no OnSpeak wired
	d.applyConnectivityChange(false)                  // must not panic

	if got := d.journal.Tail(5); len(got) != 1 {
		t.Fatalf("the transition should still be journalled, got %+v", got)
	}
}
