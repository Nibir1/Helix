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
	args := svc.captureArgs()

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
