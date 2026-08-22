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

	// Real ffmpeg output is always preferred over the guess.
	if got := describeCaptureFailure(ctx, "boom"); got != "boom" {
		t.Errorf("stderr must win when present, got %q", got)
	}
}
