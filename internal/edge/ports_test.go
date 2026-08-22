package edge

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// occupy binds a loopback port for the test's lifetime and returns it.
func occupy(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestPortAvailable(t *testing.T) {
	busy := occupy(t)
	if PortAvailable(busy) {
		t.Errorf("port %d is bound and must not read as available", busy)
	}
	// Out-of-range values must be rejected rather than attempted.
	for _, bad := range []int{0, -1, 70000} {
		if PortAvailable(bad) {
			t.Errorf("port %d is not a valid port", bad)
		}
	}
}

// TestFreePortForPrefersTheDefault: someone who launches a sidecar with no flags
// gets the upstream default, and moving Helix off it would break that stock case
// to solve a collision that may not exist here.
func TestFreePortForPrefersTheDefault(t *testing.T) {
	// Bind briefly to learn a port number the OS considers usable, then release
	// it so the port is genuinely free for the call under test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	free, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	got, isPreferred := FreePortFor("whisper-local", free)
	if !isPreferred || got != free {
		t.Errorf("a free preferred port must be kept: got %d (preferred=%v)", got, isPreferred)
	}
}

func TestFreePortForFallsBackWhenOccupied(t *testing.T) {
	busy := occupy(t)

	got, isPreferred := FreePortFor("whisper-local", busy)
	if isPreferred {
		t.Error("an occupied port must not be reported as preferred")
	}
	if got == busy {
		t.Errorf("the fallback must differ from the occupied port %d", busy)
	}
	if !PortAvailable(got) {
		t.Errorf("the suggested port %d is not actually free", got)
	}
	// Below both ephemeral ranges (Linux 32768+, macOS 49152+) so the kernel
	// cannot hand it out between the suggestion and the launch.
	if got < candidateBase || got >= candidateBase+candidateSpan {
		t.Errorf("suggested port %d is outside the intended range %d-%d",
			got, candidateBase, candidateBase+candidateSpan-1)
	}
	if got >= 32768 {
		t.Errorf("port %d sits in an ephemeral range and could be taken by the OS", got)
	}
}

// TestFreePortForIsDeterministic: re-running setup must suggest the same port,
// or the saved config stops matching and the user is asked to relaunch every
// time.
func TestFreePortForIsDeterministic(t *testing.T) {
	busy := occupy(t)

	first, _ := FreePortFor("whisper-local", busy)
	for i := 0; i < 5; i++ {
		got, _ := FreePortFor("whisper-local", busy)
		if got != first {
			t.Fatalf("suggestion varies between calls: %d then %d", first, got)
		}
	}
	// Different services get different ports, so two sidecars are not sent to
	// the same one.
	other, _ := FreePortFor("piper-local", busy)
	if other == first {
		t.Errorf("distinct services should not both be sent to %d", first)
	}
}

func TestPortOccupant(t *testing.T) {
	busy := occupy(t)
	if got := PortOccupant(busy); got == "" {
		t.Error("an occupied port must be reported as in use")
	} else if !strings.Contains(got, "in use") {
		t.Errorf("occupant description = %q", got)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	free, _ := strconv.Atoi(portStr)
	if got := PortOccupant(free); got != "" {
		t.Errorf("a free port must report nothing, got %q", got)
	}
}

func TestReplacePort(t *testing.T) {
	cases := []struct {
		in   string
		port int
		want string
	}{
		{"http://127.0.0.1:8080", 28123, "http://127.0.0.1:28123"},
		{"http://127.0.0.1:8080/v1", 28123, "http://127.0.0.1:28123/v1"},
		{"https://localhost:5000/api/tts", 5001, "https://localhost:5001/api/tts"},
		{"http://127.0.0.1", 9000, "http://127.0.0.1:9000"},
		{"127.0.0.1:8080", 9000, "http://127.0.0.1:9000"},
	}
	for _, tc := range cases {
		if got := ReplacePort(tc.in, tc.port); got != tc.want {
			t.Errorf("ReplacePort(%q, %d) = %q, want %q", tc.in, tc.port, got, tc.want)
		}
	}
}

// TestPortAvailableDetectsWildcardBind is the bug this check shipped with.
//
// Go sets SO_REUSEADDR on listeners, and on BSD/macOS that permits binding
// 127.0.0.1:P while another process holds the wildcard *:P. macOS AirPlay
// Receiver binds exactly that way, so a bind-only check reported port 5000 as
// free on a machine where AirPlay was answering HTTP 403 on it.
func TestPortAvailableDetectsWildcardBind(t *testing.T) {
	// Bind the wildcard, as a system service would.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind wildcard here: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Accept connections, so a connect probe finds a live service.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	if PortAvailable(port) {
		t.Errorf("port %d is served by a wildcard listener and must not read as free "+
			"— a bind-only check says free here because Go sets SO_REUSEADDR", port)
	}
	if got := PortOccupant(port); got == "" {
		t.Errorf("port %d must be reported as occupied", port)
	}
}

// TestPortAvailableDetectsBoundButSilent covers the other half: a socket that is
// bound and never accepts. Connect alone would call that free.
func TestPortAvailableDetectsBoundButSilent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	// Deliberately never Accept.

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	if PortAvailable(port) {
		t.Errorf("port %d is bound and must not read as free", port)
	}
}

// TestFreePortForAvoidsAWildcardHolder ties it together: the assigned port must
// be free by the corrected definition, not merely bindable.
func TestFreePortForAvoidsAWildcardHolder(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind wildcard here: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	busy, _ := strconv.Atoi(portStr)

	got, isPreferred := FreePortFor("piper-local", busy)
	if isPreferred || got == busy {
		t.Fatalf("must not hand back the occupied port %d", busy)
	}
	if !PortAvailable(got) {
		t.Errorf("assigned port %d is not actually free", got)
	}
}
