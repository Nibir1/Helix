// internal/shell/keywait_test.go
// Purpose: exercise the readiness syscall itself rather than a mock of it.
//
// The mechanism is the whole risk here: three plausible ways to pre-empt a
// blocking terminal read were tried and all three failed for reasons no unit
// test would have surfaced (Go does not register character devices with its
// poller; TIOCSTI is disabled on modern Linux). What is left is `poll(2)`, and
// a test that mocked it would prove nothing about the only question worth
// asking — does the call actually return when nothing is typed, and actually
// report ready when something is.
package shell

import (
	"os"
	"testing"
	"time"
)

// A pipe stands in for the terminal here. poll(2) does not care which kind of
// descriptor it is given, and a pipe is the one kind a test can write into
// on demand — the terminal case is covered by the PTY e2e suite, which runs the
// real binary.
func withStdin(t *testing.T, r *os.File) {
	t.Helper()
	saved := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = saved })
}

// The load-bearing property: an idle prompt must not spin. If KeyReady
// returned immediately with "not ready", the armed wait would become a busy
// loop burning a core while the shell sits doing nothing.
func TestKeyReadyReportsTimeout(t *testing.T) {
	if !KeyWaitSupported() {
		t.Skip("keystroke readiness is not implemented on this platform")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	withStdin(t, r)

	start := time.Now()
	ready, err := KeyReady(120 * time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("KeyReady: %v", err)
	}
	if ready {
		t.Error("reported input waiting on an idle descriptor")
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("returned after %v for a 120ms timeout — an armed prompt would spin", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("returned after %v for a 120ms timeout — the wake event would wait", elapsed)
	}
}

// ...and the other half: a waiting byte must be reported, and must still be
// there afterwards. Not consuming it is what lets ReadLine run unmodified.
func TestKeyReadyLeavesTheByteForTheReader(t *testing.T) {
	if !KeyWaitSupported() {
		t.Skip("keystroke readiness is not implemented on this platform")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	withStdin(t, r)

	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	ready, err := KeyReady(time.Second)
	if err != nil {
		t.Fatalf("KeyReady: %v", err)
	}
	if !ready {
		t.Fatal("a byte was waiting and readiness was not reported")
	}

	// The byte is the point. If readiness consumed it, the first character the
	// user typed would vanish from every armed prompt.
	buf := make([]byte, 1)
	n, rerr := os.Stdin.Read(buf)
	if rerr != nil || n != 1 || buf[0] != 'x' {
		t.Fatalf("read back n=%d %q err=%v — readiness must not consume the input",
			n, buf[:n], rerr)
	}
}

// ArmedWait must prefer the caller's event when both are ready, and must
// report unavailable rather than blocking when stdin is not a terminal — the
// case every test binary and every piped invocation is in.
func TestArmedWaitIsUnavailableWithoutATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	withStdin(t, r)

	other := make(chan struct{}, 1)
	res, _ := ArmedWait(other, 10*time.Millisecond)
	if res != ArmedUnavailable {
		t.Errorf("ArmedWait on a pipe = %v, want ArmedUnavailable — a non-terminal stdin "+
			"cannot be armed, and guessing would hang a piped session", res)
	}
}
