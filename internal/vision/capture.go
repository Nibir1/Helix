// internal/vision/capture.go
// Purpose: BlackBox Phase 5 (P5.2) — single-frame camera capture via an
// ffmpeg shell-out (ADR-003), downscaled to ≤1024px wide JPEG (~q80), and kept
// MEMORY-ONLY: bytes are piped through stdout (image2pipe) and never touch the
// filesystem (enforced by tests). No CGO, no gocv/OpenCV dependency.
package vision

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// VisionCaptureService grabs one camera frame per conversational turn.
type VisionCaptureService struct {
	Device string // platform device name; "" = platform default
	bin    string // ffmpeg binary path (test seam)
}

// NewCaptureService builds a capture service with the platform default device.
func NewCaptureService() *VisionCaptureService {
	return &VisionCaptureService{bin: "ffmpeg"}
}

// newCaptureServiceWithBin is the test seam for injecting a fake ffmpeg.
func newCaptureServiceWithBin(bin, device string) *VisionCaptureService {
	return &VisionCaptureService{bin: bin, Device: device}
}

// Available reports whether ffmpeg is discoverable on PATH.
func (s *VisionCaptureService) Available() bool {
	_, err := exec.LookPath(s.bin)
	return err == nil
}

// CaptureFrame grabs one frame and returns it as in-memory bytes.
func (s *VisionCaptureService) CaptureFrame(ctx context.Context) (Frame, error) {
	if !s.Available() {
		return Frame{}, fmt.Errorf("ffmpeg not found — install ffmpeg to enable /eyes")
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, s.bin, s.captureArgs()...)
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return Frame{}, fmt.Errorf("ffmpeg capture: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	if out.Len() == 0 {
		return Frame{}, fmt.Errorf("ffmpeg produced no frame data")
	}

	return Frame{
		CapturedAt:   time.Now(),
		Data:         out.Bytes(),
		SourceDevice: s.Device,
	}, nil
}

// captureArgs assembles the ffmpeg invocation. The final argument is always
// "-" (image2pipe → stdout): there is never an on-disk output file.
func (s *VisionCaptureService) captureArgs() []string {
	args := s.inputArgs()
	args = append(args,
		"-frames:v", "1",
		"-vf", "scale='min(1024,iw)':-2", // downscale to ≤1024px wide, keep aspect
		"-q:v", "4", // JPEG quality ~80
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)
	return args
}

// inputArgs returns the platform-specific input device flags.
func (s *VisionCaptureService) inputArgs() []string {
	switch runtime.GOOS {
	case "darwin":
		dev := s.Device
		if dev == "" {
			dev = "default"
		}
		return []string{"-f", "avfoundation", "-framerate", "1", "-i", dev}
	case "windows":
		dev := s.Device
		if dev == "" {
			dev = "video=Integrated Camera"
		}
		return []string{"-f", "dshow", "-i", dev}
	default:
		dev := s.Device
		if dev == "" {
			dev = "/dev/video0"
		}
		return []string{"-f", "v4l2", "-i", dev}
	}
}
