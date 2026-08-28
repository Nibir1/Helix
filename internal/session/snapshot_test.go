package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// withHome points HOME at a temp directory so the snapshot store, which
// deliberately resolves its own path, does not touch the developer's ~/.helix.
func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
	return dir
}

func sampleTurns(n int) []Turn {
	out := make([]Turn, 0, n)
	base := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		out = append(out, Turn{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Channel:   "text",
			UserText:  "question " + string(rune('a'+i)),
			Reply:     "answer " + string(rune('a'+i)),
		})
	}
	return out
}

func TestSaveAndLoadSnapshot(t *testing.T) {
	withHome(t)

	turns := sampleTurns(3)
	id, err := SaveSnapshot("test-run", turns)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("a non-empty conversation must produce a snapshot id")
	}

	snap, err := LoadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Turns) != 3 {
		t.Fatalf("loaded %d turns, want 3", len(snap.Turns))
	}
	if snap.Label != "test-run" {
		t.Errorf("label = %q, want test-run", snap.Label)
	}
	if snap.Turns[0].UserText != turns[0].UserText {
		t.Errorf("content not preserved: %q", snap.Turns[0].UserText)
	}
}

// TestSaveEmptyIsNoOp: callers archive defensively before a wipe, and an empty
// conversation has nothing to lose — that must not be an error or a stray file.
func TestSaveEmptyIsNoOp(t *testing.T) {
	withHome(t)

	id, err := SaveSnapshot("nothing", nil)
	if err != nil {
		t.Fatalf("archiving an empty conversation must not fail: %v", err)
	}
	if id != "" {
		t.Errorf("expected no snapshot id, got %q", id)
	}
	snaps, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected no snapshots on disk, found %d", len(snaps))
	}
}

func TestListSnapshotsNewestFirstAndSkipsCorrupt(t *testing.T) {
	home := withHome(t)

	dir := filepath.Join(home, ".helix", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("20260101-010101.json",
		`{"id":"20260101-010101","label":"old","created_at":"2026-01-01T01:01:01Z","turns":[{"user_text":"first"}]}`)
	write("20260601-010101.json",
		`{"id":"20260601-010101","label":"new","created_at":"2026-06-01T01:01:01Z","turns":[{"user_text":"second"}]}`)
	// A corrupt archive must not hide the healthy ones.
	write("broken.json", "{ not json")
	write("ignored.txt", "not a snapshot")

	snaps, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("listed %d snapshots, want 2 (corrupt and non-JSON skipped)", len(snaps))
	}
	if snaps[0].ID != "20260601-010101" {
		t.Errorf("newest first violated: got %s", snaps[0].ID)
	}
	if snaps[0].Preview != "second" {
		t.Errorf("preview = %q, want the first user line", snaps[0].Preview)
	}
}

// TestLoadRejectsTraversal: the id becomes a filename, so it must not be able
// to escape the snapshot directory.
func TestLoadRejectsTraversal(t *testing.T) {
	withHome(t)

	for _, id := range []string{"../../etc/passwd", "..", "", "  ", "sub/dir"} {
		if _, err := LoadSnapshot(id); err == nil {
			t.Errorf("LoadSnapshot(%q) must fail", id)
		}
		if err := DeleteSnapshot(id); err == nil {
			t.Errorf("DeleteSnapshot(%q) must fail", id)
		}
	}
}

func TestDeleteSnapshot(t *testing.T) {
	withHome(t)

	id, err := SaveSnapshot("temp", sampleTurns(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteSnapshot(id); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(id); err == nil {
		t.Error("the snapshot should be gone")
	}
}

func TestSnapshotFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	home := withHome(t)

	id, err := SaveSnapshot("perm", sampleTurns(1))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, ".helix", "sessions", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	// A transcript holds whatever the user typed; it gets the same protection
	// as the live session file.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot mode = %o, want 600", perm)
	}
}

func TestRingStoreRestoreAndCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store, err := NewRingStoreAt(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if store.Capacity() != 3 {
		t.Fatalf("capacity = %d, want 3", store.Capacity())
	}

	// Restoring more than the ring holds must keep the MOST RECENT turns:
	// dropping the newest would discard the context that still matters.
	if err := store.Restore(sampleTurns(5)); err != nil {
		t.Fatal(err)
	}
	got := store.Recent(store.Len())
	if len(got) != 3 {
		t.Fatalf("kept %d turns, want 3", len(got))
	}
	if got[0].UserText != "question c" || got[2].UserText != "question e" {
		t.Errorf("kept the wrong window: %q .. %q", got[0].UserText, got[2].UserText)
	}

	// And it must survive a reload, since /resume has to outlive the process.
	reloaded, err := NewRingStoreAt(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Len() != 3 {
		t.Errorf("reload has %d turns, want 3", reloaded.Len())
	}

	if err := store.Restore(nil); err != nil {
		t.Fatal(err)
	}
	if store.Len() != 0 {
		t.Errorf("restoring nothing should empty the ring, got %d", store.Len())
	}
}
