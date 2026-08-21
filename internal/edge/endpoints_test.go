package edge

import (
	"strings"
	"testing"
)

func TestFindConflictsDetectsSharedPort(t *testing.T) {
	// The real case: llama.cpp and whisper.cpp both default to 8080.
	got := FindConflicts([]Endpoint{
		{Service: "llama.cpp", Role: "LLM", URL: "http://127.0.0.1:8080/v1", Active: true},
		{Service: "whisper-local", Role: "STT", URL: "http://127.0.0.1:8080", Active: true},
		{Service: "piper-local", Role: "TTS", URL: "http://127.0.0.1:5000", Active: true},
	})
	if len(got) != 1 {
		t.Fatalf("found %d conflicts, want 1: %+v", len(got), got)
	}
	if got[0].Address != "127.0.0.1:8080" {
		t.Errorf("address = %q", got[0].Address)
	}
	if !got[0].Involves() {
		t.Error("a conflict between two active services must report Involves()")
	}
	desc := got[0].Describe()
	if !strings.Contains(desc, "llama.cpp") || !strings.Contains(desc, "whisper-local") {
		t.Errorf("description must name both services: %q", desc)
	}
}

// TestFindConflictsIgnoresDifferingPaths: one process owns a listener, so the
// path is irrelevant to whether two services collide.
func TestFindConflictsIgnoresDifferingPaths(t *testing.T) {
	got := FindConflicts([]Endpoint{
		{Service: "a", URL: "http://127.0.0.1:8080/v1"},
		{Service: "b", URL: "http://127.0.0.1:8080/inference"},
	})
	if len(got) != 1 {
		t.Fatalf("differing paths on one port must still conflict: %+v", got)
	}
}

// TestFindConflictsNormalizesLoopback: the shipped defaults mix "localhost" and
// "127.0.0.1" spellings, and they are the same listener.
func TestFindConflictsNormalizesLoopback(t *testing.T) {
	got := FindConflicts([]Endpoint{
		{Service: "a", URL: "http://localhost:8080"},
		{Service: "b", URL: "http://127.0.0.1:8080"},
	})
	if len(got) != 1 {
		t.Fatalf("localhost and 127.0.0.1 must compare equal: %+v", got)
	}
}

func TestFindConflictsFillsDefaultPorts(t *testing.T) {
	got := FindConflicts([]Endpoint{
		{Service: "a", URL: "http://example.com"},
		{Service: "b", URL: "http://example.com:80"},
	})
	if len(got) != 1 {
		t.Fatalf("an implied default port must compare equal: %+v", got)
	}
	if got[0].Address != "example.com:80" {
		t.Errorf("address = %q, want example.com:80", got[0].Address)
	}
}

func TestFindConflictsNoFalsePositives(t *testing.T) {
	cases := [][]Endpoint{
		nil,
		{{Service: "only", URL: "http://127.0.0.1:8080"}},
		{
			{Service: "a", URL: "http://127.0.0.1:8080"},
			{Service: "b", URL: "http://127.0.0.1:8081"},
			{Service: "c", URL: "http://127.0.0.1:5000"},
		},
		// The same service listed twice is not a conflict with itself.
		{
			{Service: "dup", URL: "http://127.0.0.1:8080"},
			{Service: "dup", URL: "http://127.0.0.1:8080"},
		},
		// Unparseable entries are skipped, not treated as a shared empty address.
		{
			{Service: "a", URL: ""},
			{Service: "b", URL: "not a url at all"},
		},
	}
	for i, in := range cases {
		if got := FindConflicts(in); len(got) != 0 {
			t.Errorf("case %d: expected no conflicts, got %+v", i, got)
		}
	}
}

// TestInvolvesDistinguishesActive: an overlap between two unselected services is
// worth a note, not a warning.
func TestInvolvesDistinguishesActive(t *testing.T) {
	got := FindConflicts([]Endpoint{
		{Service: "a", Role: "STT", URL: "http://127.0.0.1:9000", Active: false},
		{Service: "b", Role: "TTS", URL: "http://127.0.0.1:9000", Active: false},
	})
	if len(got) != 1 {
		t.Fatalf("expected the overlap to be reported: %+v", got)
	}
	if got[0].Involves() {
		t.Error("no active service means Involves() must be false")
	}
	if !strings.Contains(got[0].Describe(), "not selected") {
		t.Errorf("description should mark the inactive services: %q", got[0].Describe())
	}
}

func TestFindConflictsIsDeterministic(t *testing.T) {
	in := []Endpoint{
		{Service: "zebra", URL: "http://127.0.0.1:8080"},
		{Service: "alpha", URL: "http://127.0.0.1:8080"},
		{Service: "beta", URL: "http://127.0.0.1:7000"},
		{Service: "gamma", URL: "http://127.0.0.1:7000"},
	}
	first := FindConflicts(in)
	for i := 0; i < 20; i++ {
		got := FindConflicts(in)
		if len(got) != len(first) {
			t.Fatalf("conflict count varies between runs")
		}
		for j := range got {
			if got[j].Address != first[j].Address {
				t.Fatalf("conflict order varies between runs: %s vs %s",
					got[j].Address, first[j].Address)
			}
			if got[j].Endpoints[0].Service != first[j].Endpoints[0].Service {
				t.Fatalf("endpoint order varies between runs")
			}
		}
	}
	// Endpoints within a conflict are sorted by service name.
	if first[1].Endpoints[0].Service != "alpha" {
		t.Errorf("endpoints not sorted: %+v", first[1].Endpoints)
	}
}
