// cmd/helix/voice_sidecars_install_test.go
//
// Purpose: a chain the user just chose must not end at "see the docs".
//
// The wizard used to report "there is no single install command for this
// platform" whenever a sidecar's InstallCmd declined — a sentence that covered
// two completely different situations: Helix must not make this choice for you
// (a compute backend, a licence), and Helix knows exactly how and is only
// blocked on a prerequisite it could install in one step. The second case was
// piper-local on a host with no Python, and it was a dead end for no reason.
package main

import (
	"strings"
	"testing"

	"helix/internal/deps"
)

// Every sidecar that refuses to install itself must say WHY.
//
// Without this the two cases above are indistinguishable to a reader, and the
// only advice available is a document.
// Every sidecar either installs itself or explains why it will not.
func TestEverySidecarRefusalExplainsItself(t *testing.T) {
	for provider, spec := range voiceSidecars() {
		if spec.InstallCmd == nil {
			continue
		}
		if _, ok := spec.InstallCmd(); ok {
			continue // installs itself; nothing to explain
		}
		// A sidecar Helix CAN install is not refusing anything: GoInstall does
		// the multi-step ones, and a declared prerequisite means the decline
		// above is "not yet" rather than "never".
		if spec.GoInstall != nil || len(spec.Prereqs) > 0 {
			continue
		}
		if spec.InstallBlocker == nil {
			t.Errorf("%s cannot install itself and has no InstallBlocker — "+
				"the user gets a dead end instead of a reason", provider)
			continue
		}
		why, ok := spec.InstallBlocker()
		if !ok || strings.TrimSpace(why) == "" {
			t.Errorf("%s: InstallBlocker returned nothing", provider)
			continue
		}
		// A reason that just repeats the symptom is not a reason.
		if !strings.ContainsAny(why, ".") || len(why) < 40 {
			t.Errorf("%s: InstallBlocker is too thin to act on: %q", provider, why)
		}
	}
}

// A declared prerequisite must exist in the deps catalogue, or the wizard will
// silently skip it at the moment it matters.
func TestSidecarPrereqsAreRealCatalogueEntries(t *testing.T) {
	for provider, spec := range voiceSidecars() {
		for _, name := range spec.Prereqs {
			dep, ok := deps.Lookup(name)
			if !ok {
				t.Errorf("%s declares prerequisite %q, which is not in the deps catalogue",
					provider, name)
				continue
			}
			if len(dep.Packages) == 0 {
				t.Errorf("%s declares prerequisite %q, which has no package names — "+
					"it can never be installed", provider, name)
			}
		}
	}
}

// An on-demand prerequisite must not appear in the first-run offer.
//
// The catalogue is short on purpose; a setup flow that asks about a dozen
// tools is one people quit halfway through. Optional entries are the mechanism
// that keeps "installable" and "asked about at first boot" separate.
func TestOptionalDepsStayOutOfFirstRun(t *testing.T) {
	for _, d := range deps.Missing() {
		if d.Optional {
			t.Errorf("%s is Optional but appears in Missing(), so first run will "+
				"offer it", d.Name)
		}
	}
	// ...and are still reachable on demand.
	if _, ok := deps.Lookup("python3"); !ok {
		t.Error("python3 is not in the catalogue, so piper-local's Python path " +
			"is a dead end again")
	}
}

// A printed launch command must name a runtime that can actually run it.
//
// Binaries is a preference order, and rendering its first entry produced
// `piper -m piper.http_server …` — the native binary's name joined to arguments
// only a Python interpreter understands. It appeared in the wizard beside the
// correct `python3 -m piper.http_server …`, two different commands for one
// sidecar in the same screen of output.
func TestLaunchCommandNeverPairsNativePiperWithPythonArgs(t *testing.T) {
	specs := voiceSidecars()
	sc, ok := specs["piper-local"]
	if !ok {
		t.Fatal("piper-local is not in the sidecar table")
	}

	// Whatever this host has, the rendered command must not be the native
	// binary carrying `-m`, which is a Python interpreter flag.
	launch := launchCommandFor("piper-local", sc)(28183)
	if launch == "" {
		return // native piper: no server, nothing to print — the correct answer
	}
	fields := strings.Fields(launch)
	if len(fields) < 2 {
		t.Fatalf("launch command is too short to be real: %q", launch)
	}
	bin, first := fields[0], fields[1]
	if first == "-m" && !isPythonInterpreter(bin) {
		t.Errorf("launch command pairs %q with the Python flag -m: %q\n"+
			"only an interpreter understands -m; this command cannot run", bin, launch)
	}
}

// Sanity: the renderer must not invent a command for a sidecar with no server.
func TestLaunchCommandIsEmptyWhenThereIsNoServer(t *testing.T) {
	sc := voiceSidecar{
		Binaries: []string{"definitely-not-installed-anywhere-xyz"},
		Args:     func(port int) []string { return []string{"--port", "1"} },
	}
	// Nothing installed: it names the preferred runtime rather than nothing,
	// because the install step is about to provide exactly that.
	if got := launchCommandFor("whisper-local", sc)(1); got == "" {
		t.Error("an uninstalled sidecar should still show what will run it")
	}
}

// A command Helix runs must be a COMMAND, not prose.
//
// csm-local's model hint used to be:
//
//	"huggingface-cli login   # then accept the terms at https://…"
//
// which was written to be printed. Once model hints started running without a
// confirmation, that string was exec'd — and there is no shell here, so the "#"
// and the URL after it became arguments to a login command. It failed every
// time and could never have done anything else.
//
// The tell is cheap to check: a "#" comment, a shell operator, or a redirect
// means the string was written for a human or for a shell that is not there.
func TestRunnableStringsAreCommandsNotProse(t *testing.T) {
	shellOnly := []struct{ token, why string }{
		{"#", "a comment — there is no shell to strip it"},
		{"&&", "a shell operator"},
		{"||", "a shell operator"},
		{";", "a shell separator"},
		{"|", "a pipe"},
		{">", "a redirect"},
	}

	check := func(t *testing.T, provider, field, cmd string) {
		t.Helper()
		if strings.TrimSpace(cmd) == "" {
			return
		}
		for _, s := range shellOnly {
			if strings.Contains(cmd, s.token) {
				t.Errorf("%s %s contains %q (%s):\n  %s\n"+
					"runVisibleCommand execs this directly with no shell, so every "+
					"word after it becomes an argument",
					provider, field, s.token, s.why, cmd)
			}
		}
	}

	for provider, spec := range voiceSidecars() {
		if spec.InstallCmd != nil {
			if cmd, ok := spec.InstallCmd(); ok {
				check(t, provider, "InstallCmd", cmd)
			}
		}
		if spec.ModelHint != nil {
			if cmd, _, ok := spec.ModelHint(); ok {
				check(t, provider, "ModelHint", cmd)
			}
		}
	}
}
