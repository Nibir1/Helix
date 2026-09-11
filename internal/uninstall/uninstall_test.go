// internal/uninstall/uninstall_test.go
// Purpose: an uninstall is irreversible and runs once, so the cheap tests are
// the only ones anybody gets.
//
// Everything here works on a temp directory. Nothing touches /etc/shells, the
// real Helix home, or a login shell — §9 rule 1's spirit: a test that can break
// the machine it runs on is not a test.
package uninstall

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain quarantines the system shells file for the WHOLE package, so no test
// can reach the real /etc/shells even by forgetting to.
//
// This is not caution. The first version of this package made etcShells a
// const, so Plan read the real file whatever home it was handed: the
// "empty machine" case returned one item because this laptop has a Helix line
// in /etc/shells, and the wholesale-removal test went on to REWRITE it. It was
// stopped by not being root. §9 rule 1 keeps audio hardware out of the suite;
// the general form is that a test which can damage the machine running it is
// not a test, and a hardcoded system path is how that happens quietly.
func TestMain(m *testing.M) {
	quarantineSystemPaths()
	os.Exit(m.Run())
}

// Plan must list only what EXISTS. A manifest padded with absent rows is read
// immediately before an irreversible yes, and padding is how the one row that
// matters gets skimmed.
func TestPlanListsOnlyWhatExists(t *testing.T) {
	home := t.TempDir()
	if got := Plan(home, filepath.Join(home, "nope", "helix")); len(got) != 0 {
		t.Fatalf("Plan on an empty machine returned %d items: %+v", len(got), got)
	}

	mkdir(t, filepath.Join(home, ".helix"))
	write(t, filepath.Join(home, ".helix_history"), "ls\n")
	exe := filepath.Join(home, "bin", "helix")
	mkdir(t, filepath.Dir(exe))
	write(t, exe, "#!/bin/sh\n")

	items := Plan(home, exe)
	want := map[string]bool{
		filepath.Join(home, ".helix"):         true,
		filepath.Join(home, ".helix_history"): true,
		exe:                                   true,
	}
	for _, it := range items {
		delete(want, it.Path)
	}
	if len(want) != 0 {
		t.Errorf("Plan missed: %v", keys(want))
	}
}

// The self-updater leaves siblings beside the binary. A `.prev` left behind is
// a 27 MB file nobody will ever look for again.
func TestPlanCollectsUpdaterLeftovers(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "bin", "helix")
	mkdir(t, filepath.Dir(exe))
	for _, name := range []string{"helix", "helix.prev", "helix.rollback", ".helix-update-staged"} {
		write(t, filepath.Join(filepath.Dir(exe), name), "x")
	}
	var found int
	for _, it := range Plan(home, exe) {
		if it.Kind == KindBinary {
			found++
		}
	}
	if found != 4 {
		t.Errorf("Plan found %d binary-kind items, want 4 (the binary plus three updater leftovers)", found)
	}
}

// The Helix home goes WHOLESALE. /purge enumerates because it names what it
// takes; an uninstall must not, because the enumeration is already incomplete —
// models.json, shell_pref and a hand-dropped key file are all in ~/.helix and
// none of them is in purge.go's list.
func TestTheHelixHomeIsRemovedWholesaleNotFileByFile(t *testing.T) {
	home := t.TempDir()
	helix := filepath.Join(home, ".helix")
	mkdir(t, helix)
	// Three files that purge.go does NOT name individually.
	for _, name := range []string{"models.json", "shell_pref", "openai.key"} {
		write(t, filepath.Join(helix, name), "secret")
	}
	items := Plan(home, "")
	var hasHome bool
	for _, it := range items {
		if it.Path == helix {
			hasHome = true
		}
		if strings.HasPrefix(it.Path, helix+string(os.PathSeparator)) {
			t.Errorf("Plan enumerates inside the Helix home (%s); it must take the directory "+
				"whole, or a file nobody remembered to list survives an uninstall", it.Path)
		}
	}
	if !hasHome {
		t.Fatal("Plan does not remove the Helix home")
	}

	if _, failures := Apply(items); len(failures) != 0 {
		t.Fatalf("Apply: %v", failures)
	}
	if _, err := os.Stat(helix); !os.IsNotExist(err) {
		t.Error("the Helix home survived, and with it every credential in it")
	}
}

// keepData is /purge's mode. The binary must still go.
func TestApplyRemovesTheBinary(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "bin", "helix")
	mkdir(t, filepath.Dir(exe))
	write(t, exe, "binary")

	removed, failures := Apply(Plan(home, exe))
	if len(failures) != 0 {
		t.Fatalf("Apply: %v", failures)
	}
	if removed != 1 {
		t.Errorf("removed %d items, want 1", removed)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) {
		t.Error("the binary survived")
	}
}

// /etc/shells loses ONE LINE. Deleting the file would unregister every other
// shell on the machine, and a truncated one makes chsh refuse all of them.
func TestDropLineKeepsEveryOtherShell(t *testing.T) {
	dir := t.TempDir()
	shells := filepath.Join(dir, "shells")
	write(t, shells, "# comment\n/bin/sh\n/bin/zsh\n/usr/local/bin/helix\n/bin/bash\n")

	if err := dropLine(shells, "/usr/local/bin/helix"); err != nil {
		t.Fatal(err)
	}
	got := read(t, shells)
	if strings.Contains(got, "helix") {
		t.Errorf("the helix line survived:\n%s", got)
	}
	for _, keep := range []string{"# comment", "/bin/sh", "/bin/zsh", "/bin/bash"} {
		if !strings.Contains(got, keep) {
			t.Errorf("dropLine removed %q as well:\n%s", keep, got)
		}
	}
}

func TestDropLineIsANoOpWhenTheLineIsAbsent(t *testing.T) {
	dir := t.TempDir()
	shells := filepath.Join(dir, "shells")
	const original = "/bin/sh\n/bin/zsh\n"
	write(t, shells, original)
	if err := dropLine(shells, "/usr/local/bin/helix"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, shells); got != original {
		t.Errorf("dropLine rewrote a file it had no line to remove from:\n%q", got)
	}
}

// dropLine must not preserve the file's mode by accident — /etc/shells is
// world-readable and a 0600 rewrite would break every other shell's lookup.
func TestDropLinePreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	shells := filepath.Join(dir, "shells")
	write(t, shells, "/bin/sh\n/usr/local/bin/helix\n")
	if err := os.Chmod(shells, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropLine(shells, "/usr/local/bin/helix"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(shells)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode is %o after the edit, want 644 — other shells would stop resolving",
			info.Mode().Perm())
	}
}

// THE LOCKOUT RULE. If the login shell cannot be restored, the binary must
// SURVIVE: the two failures compose into a machine that cannot open a terminal.
func TestAFailedShellRestoreKeepsTheBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "helix")
	write(t, exe, "binary")

	items := []Item{
		// A shell item that cannot succeed: Edit with a file that is not there.
		{Path: filepath.Join(dir, "absent-shells"), EditLine: "/x", Kind: KindShell, Edit: true},
		{Path: exe, Kind: KindBinary},
	}
	removed, failures := Apply(items)
	if removed != 0 {
		t.Errorf("removed %d items; the binary must be kept when the shell step fails", removed)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatal("THE BINARY WAS REMOVED after the login shell could not be restored — " +
			"this is the state where no new terminal will start")
	}
	if len(failures) != 2 {
		t.Errorf("failures = %v, want both the shell failure and the deliberate binary skip", failures)
	}
	if !strings.Contains(strings.Join(failures, " "), "unable to open a terminal") {
		t.Error("the kept-binary failure does not explain why it was kept")
	}
}

// A failure must not stop the rest: a half-uninstall with no report leaves the
// user unable to finish.
func TestApplyContinuesPastAFailure(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	write(t, good, "x")
	items := []Item{
		{Path: filepath.Join(dir, "absent"), Kind: KindService, Edit: true, EditLine: "x"},
		{Path: good, Kind: KindData},
	}
	removed, failures := Apply(items)
	if removed != 1 {
		t.Errorf("removed %d, want 1 — Apply stopped at the first failure", removed)
	}
	if len(failures) != 1 {
		t.Errorf("failures = %v, want exactly the absent one", failures)
	}
}

// The service paths must match what cmd/helix/daemon_cmd.go actually writes.
// This package cannot import cmd/helix, so the guard reads the source: a
// drifted copy here leaves a launchd job pointing at a deleted binary, retried
// and logged forever.
func TestServicePathsMatchTheDaemonsOwn(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "helix", "daemon_cmd.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	switch runtime.GOOS {
	case "darwin":
		for _, frag := range []string{`"Library", "LaunchAgents"`, `serviceLabel = "com.helix.daemon"`} {
			if !strings.Contains(body, frag) {
				t.Errorf("daemon_cmd.go no longer contains %s — planServices is now stale "+
					"and would leave the launchd job behind", frag)
			}
		}
	case "linux":
		if !strings.Contains(body, `".config", "systemd", "user", "helix-daemon.service"`) {
			t.Error("daemon_cmd.go's systemd unit path changed; planServices is now stale")
		}
	}
}

func TestNeedsRootAnyReportsTheWholePlan(t *testing.T) {
	if NeedsRootAny(nil) {
		t.Error("an empty plan needs root")
	}
	if NeedsRootAny([]Item{{Path: "/a"}, {Path: "/b"}}) {
		t.Error("a plan with no root-owned item needs root")
	}
	if !NeedsRootAny([]Item{{Path: "/a"}, {Path: "/b", NeedsRoot: true}}) {
		t.Error("a plan containing a root-owned item does not need root")
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
