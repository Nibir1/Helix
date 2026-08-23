// internal/vision/capture_test.go
// Purpose: Phase 5 (P5.6) — prove frame capture is memory-only (stdout pipe,
// no on-disk output), round-trips through a fake ffmpeg, and skips cleanly
// when ffmpeg is absent.
package vision

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCaptureArgsTargetStdout(t *testing.T) {
	svc := newCaptureServiceWithBin("ffmpeg", "")
	args := svc.captureArgs("")

	if len(args) == 0 || args[len(args)-1] != "-" {
		t.Fatalf("capture must pipe to stdout ('-' last), got %v", args)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "image2pipe") {
		t.Fatalf("capture must use image2pipe (no file output): %v", args)
	}
	for _, a := range args {
		if a == "-y" || strings.HasSuffix(a, ".jpg") || strings.HasSuffix(a, ".jpeg") || strings.HasSuffix(a, ".png") {
			t.Fatalf("capture args must never reference an output file: %v", args)
		}
	}
}

func TestCaptureFrameRoundTripMemoryOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture needs POSIX sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ffmpeg")
	// Fake ffmpeg: emits bytes on stdout only, writes nothing to disk.
	script := "#!/bin/sh\nprintf 'FAKEJPEG'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := newCaptureServiceWithBin(bin, "")
	frame, err := svc.CaptureFrame(context.Background())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if string(frame.Data) != "FAKEJPEG" {
		t.Fatalf("frame data = %q", frame.Data)
	}

	// The only file in the temp dir must be the fake ffmpeg itself — Helix's
	// capture path wrote nothing to disk.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ffmpeg" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("capture persisted files to disk: %v", names)
	}
}

func TestCaptureUnavailableFailsCleanly(t *testing.T) {
	svc := newCaptureServiceWithBin("definitely-not-a-real-ffmpeg-binary", "")
	if svc.Available() {
		t.Fatal("unexpected availability")
	}
	if _, err := svc.CaptureFrame(context.Background()); err == nil {
		t.Fatal("expected a clear error when ffmpeg is absent")
	}
}

// TestNegotiateFramerateReadsTheDeviceOwnAnswer covers the bug that made camera
// capture impossible on a MacBook Air: a hardcoded "-framerate 1" that no
// webcam accepts. ffmpeg names the rates it will take, so the retry reads them
// instead of guessing again.
func TestNegotiateFramerateReadsTheDeviceOwnAnswer(t *testing.T) {
	const rejection = `[in#0 @ 0x88d024000] Selected framerate (1.000000) is not supported by the device.
[in#0 @ 0x88d024000] Supported modes:
[in#0 @ 0x88d024000]   640x480@[15.000000 30.000000]fps
[in#0 @ 0x88d024000]   1920x1080@[15.000000 30.000000]fps
[in#0 @ 0x88cc10000] Error opening input: Input/output error`

	rate, ok := negotiateFramerate(rejection)
	if !ok {
		t.Fatal("a rejection that lists supported modes must yield a rate")
	}
	// Highest advertised: one frame is taken either way, and a faster mode
	// reaches it sooner.
	if rate != "30.000000" {
		t.Errorf("rate = %q, want the highest advertised (30)", rate)
	}

	// An unrelated failure must NOT be retried as if it were a rate problem.
	for _, other := range []string{
		"", "Error opening input file default.",
		"[in#0] Selected pixel format (yuv420p) is not supported by the input device.",
	} {
		if _, ok := negotiateFramerate(other); ok {
			t.Errorf("negotiated a framerate from an unrelated error: %q", other)
		}
	}
}

// A silent, output-less failure is the signature of an unauthorized camera on
// macOS, and saying so is the difference between a fixable problem and a
// mystery.
func TestCaptureFailureNamesTheLikelyCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stands in for a deadline that expired with no ffmpeg output

	msg := describeCaptureFailure(ctx, "")
	if msg == "" {
		t.Fatal("a silent failure still needs an explanation")
	}
	if runtime.GOOS == "darwin" && !strings.Contains(msg, "Privacy & Security") {
		t.Errorf("on macOS the message should name the permission screen: %q", msg)
	}

	// EXPECTATION CHANGED (2026-08-23), on evidence rather than convenience.
	//
	// This used to assert that stderr wins outright whenever present. Measured
	// twice against a real camera the OS had not authorized, that policy reported
	// the wrong cause both times: first ffmpeg's version banner (which it writes
	// on every run), then — after -hide_banner — a non-fatal pixel-format warning
	// it self-corrects from and continues past. Neither was why the capture
	// failed; the expired deadline was.
	//
	// So when the deadline expired, the diagnosis must LEAD with the deadline and
	// may carry ffmpeg's words as detail. The original intent — never discard real
	// ffmpeg output — is preserved by keeping it in the message, and asserted
	// directly in the healthy-context case below.
	if got := describeCaptureFailure(ctx, "boom"); !strings.Contains(got, "boom") {
		t.Errorf("ffmpeg output must survive into the message, got %q", got)
	}
	if got := describeCaptureFailure(ctx, "boom"); got == "boom" {
		t.Errorf("an expired deadline is the cause and must lead the message, got %q", got)
	}

	// With a healthy context, ffmpeg exited on its own terms and its output IS
	// the cause — unchanged, and the case the old assertion was really about.
	live, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	if got := describeCaptureFailure(live, "boom"); got != "boom" {
		t.Errorf("stderr must win when ffmpeg exited on its own, got %q", got)
	}
	if got := describeCaptureFailure(live, ""); got != "ffmpeg exited without output" {
		t.Errorf("a silent self-exit needs its own message, got %q", got)
	}
}

// The capture step is bounded on its own, not by whatever the caller allowed for
// the whole turn.
//
// Both call sites pass 30s, which is right for a turn and far too long for a
// device open: measured against a denied camera that was thirty seconds of
// silence, which is exactly the "looks like a hang" this path exists to avoid.
func TestCaptureDeadlineIsShorterThanATurn(t *testing.T) {
	if CaptureDeadline <= 0 {
		t.Fatal("a capture must be bounded")
	}
	if CaptureDeadline >= 30*time.Second {
		t.Errorf("CaptureDeadline %v is not shorter than the 30s turn budget the "+
			"callers pass — a denied camera would stall the whole turn", CaptureDeadline)
	}
	// Generous against real hardware: a working camera yields a frame in well
	// under a second, a cold USB webcam in a couple.
	if CaptureDeadline < 3*time.Second {
		t.Errorf("CaptureDeadline %v is tight enough to fail a slow-warming "+
			"webcam", CaptureDeadline)
	}
}

// -hide_banner must stay on every platform's input args. Without it ffmpeg writes
// its version and build configuration to stderr on every run, which is what made
// the failure diagnosis unreachable in the first place.
func TestCaptureSuppressesFFmpegBanner(t *testing.T) {
	svc := NewCaptureService()
	args := svc.captureArgs("30")

	var found bool
	for _, a := range args {
		if a == "-hide_banner" {
			found = true
		}
	}
	if !found {
		t.Errorf("capture args must include -hide_banner, got %v", args)
	}
}

// fakeFFmpeg writes a script standing in for ffmpeg and returns a service using
// it. body is shell run by the fake.
func fakeFFmpeg(t *testing.T, body string) *VisionCaptureService {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture needs POSIX sh")
	}
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return newCaptureServiceWithBin(bin, "")
}

// A capture service must not accuse the camera before anything has been tried:
// a fresh session has simply not looked yet. This is what keeps the status line
// from reading "no frames" on a machine that has not been asked for one.
func TestLastFailureStartsUnknown(t *testing.T) {
	svc := NewCaptureService()
	err, everWorked := svc.LastFailure()
	if err != nil {
		t.Errorf("a fresh service has no failure to report, got %v", err)
	}
	if everWorked {
		t.Error("a fresh service has not succeeded either")
	}
}

// A real failure must be remembered, so a status report can stop claiming the
// camera is watching when it has never produced a frame.
func TestLastFailureRemembersAFailedCapture(t *testing.T) {
	svc := fakeFFmpeg(t, "echo 'device busy' >&2; exit 1")

	if _, err := svc.CaptureFrame(context.Background()); err == nil {
		t.Fatal("a non-zero ffmpeg exit must fail the capture")
	}
	err, everWorked := svc.LastFailure()
	if err == nil {
		t.Fatal("the failure must be remembered for the status report")
	}
	if everWorked {
		t.Error("nothing has succeeded, so everWorked must stay false")
	}
	if !strings.Contains(err.Error(), "device busy") {
		t.Errorf("the remembered error should carry ffmpeg's own words, got %v", err)
	}
}

// An empty frame counts as a failure: ffmpeg exiting cleanly with no bytes is
// exactly what an unauthorized camera looks like, and reporting "watching" for
// it is the overstatement this tracking exists to prevent.
func TestLastFailureCountsAnEmptyFrame(t *testing.T) {
	svc := fakeFFmpeg(t, "exit 0") // clean exit, no bytes

	if _, err := svc.CaptureFrame(context.Background()); err == nil {
		t.Fatal("a capture producing no bytes must fail")
	}
	if err, everWorked := svc.LastFailure(); err == nil || everWorked {
		t.Errorf("an empty frame must be remembered as a failure (err=%v ok=%v)",
			err, everWorked)
	}
}

// Success must clear the accusation, so a camera that starts working after a
// permission grant stops being reported as broken.
func TestLastFailureClearedBySuccess(t *testing.T) {
	svc := fakeFFmpeg(t, "printf 'FAKEJPEG'")

	if _, err := svc.CaptureFrame(context.Background()); err != nil {
		t.Fatalf("fake capture should succeed: %v", err)
	}
	err, everWorked := svc.LastFailure()
	if err != nil {
		t.Errorf("a successful capture must clear the last error, got %v", err)
	}
	if !everWorked {
		t.Error("a successful capture must be remembered")
	}
}
