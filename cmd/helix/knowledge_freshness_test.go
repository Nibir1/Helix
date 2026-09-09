// cmd/helix/knowledge_freshness_test.go
// Purpose: /status must not claim a history it cannot see.
//
// From a user's own screen:
//
//	KNOWLEDGE  16000 CVEs · 46687 exploits · 1699 KEV · 858 MITRE · updated never
//
// Tens of thousands of records that arrived from nowhere. The timestamp was
// stamped by one of UpdateAll's three callers, so `/knowledge-update` and the
// first-run path filled the database and left it unset — fixed in UpdateAll.
// These tests cover the other half: an existing database still carries no
// timestamp, so the report has to distinguish "never happened" from "happened,
// not recorded".
package main

import (
	"strings"
	"testing"

	"helix/internal/audio"
	"helix/internal/config"
	"helix/internal/shell"
)

func TestKnowledgeFreshnessDistinguishesThreeStates(t *testing.T) {
	// A recorded timestamp is reported as itself.
	if got := knowledgeFreshness("2026-09-09T12:00:00Z", 64253); !strings.Contains(got, "2026-09-09") {
		t.Errorf("with a timestamp, got %q — want the timestamp", got)
	}

	// An empty corpus genuinely has never been updated.
	got := knowledgeFreshness("", 0)
	if !strings.Contains(got, "never") {
		t.Errorf("empty corpus with no timestamp = %q, want never", got)
	}

	// A POPULATED corpus with no timestamp is the case that produced the bug.
	// It must not say never, and it must say what to do.
	got = knowledgeFreshness("", 64253)
	if strings.Contains(got, "never") {
		t.Errorf("populated corpus reported as %q — 64,253 records did not arrive from "+
			"nowhere, and a report that says they did is worse than one that says it "+
			"does not know", got)
	}
	if !strings.Contains(got, "not recorded") {
		t.Errorf("populated corpus with no timestamp = %q, want it to say the time is "+
			"unrecorded rather than absent", got)
	}
}

// The emptiness test must read the SAME numbers the line prints, or the two
// halves of one sentence could disagree.
func TestStatTotalCountsEveryPrintedCorpus(t *testing.T) {
	stats := map[string]interface{}{
		"db_cves":     16000,
		"db_exploits": 46687,
		"db_kev":      1699,
		"db_mitre":    858,
		"unrelated":   "ignored",
	}
	if got, want := statTotal(stats), 16000+46687+1699+858; got != want {
		t.Errorf("statTotal = %d, want %d", got, want)
	}
	if got := statTotal(map[string]interface{}{}); got != 0 {
		t.Errorf("statTotal of no stats = %d, want 0", got)
	}
	// A missing or wrongly-typed count must not be read as records. Counting a
	// nil as data would flip an empty corpus into "populated" and report the
	// opposite of the truth.
	if got := statTotal(map[string]interface{}{"db_cves": nil, "db_kev": "12"}); got != 0 {
		t.Errorf("statTotal with untyped values = %d, want 0", got)
	}
}

// The TOGGLES row must report DEVIATIONS, and "all at defaults" must be
// reachable.
//
// From the same /status a user pasted:
//
//	TOGGLES  audio · stealth
//
// Both of those default to ON, so a healthy machine always printed them and
// the line never said anything. Two consequences: the states worth seeing
// (audio off, private execution off) were invisible, and the one line meaning
// "nothing unusual here" could not print on any host where both work.
func TestToggleLineReportsDeviationsNotDefaults(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = &config.Config{}

	// audio defaults ON and agentCore is nil in this test binary, so a default
	// session must produce the quiet line.
	if !audio.IsEnabled() {
		t.Skip("audio is disabled in this environment; the default case cannot be checked")
	}
	got := shell.Plain(sessionToggleLine())
	if !strings.Contains(got, "all at defaults") {
		t.Errorf("a default session printed %q — the row is for what is UNUSUAL, and "+
			"listing on-by-default switches means it can never say nothing is", got)
	}
	if strings.Contains(got, "audio") {
		t.Errorf("printed %q — audio is on by default, so naming it is noise; the "+
			"reportable state is audio OFF", got)
	}

	// A genuine deviation must appear.
	cfg.UserPrefs.TypewriteAll = true
	got = shell.Plain(sessionToggleLine())
	if !strings.Contains(got, "typewrite-all") {
		t.Errorf("with typewrite-all on, got %q — a real deviation must be named", got)
	}
	if strings.Contains(got, "all at defaults") {
		t.Errorf("got %q — it cannot be both", got)
	}
}
