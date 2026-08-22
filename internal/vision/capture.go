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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// VisionCaptureService grabs one camera frame per conversational turn.
type VisionCaptureService struct {
	Device string // platform device name; "" = platform default
	bin    string // ffmpeg binary path (test seam)

	// framerate is the rate this device accepted, learned from its own
	// rejection on the first capture and reused afterwards. Empty means "not
	// negotiated yet — let ffmpeg choose".
	framerate string
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
//
// The capture rate is NEGOTIATED, not assumed — the same lesson the provider
// adapters learned about max_tokens. See negotiateFramerate for what a guessed
// value costs.
func (s *VisionCaptureService) CaptureFrame(ctx context.Context) (Frame, error) {
	if !s.Available() {
		return Frame{}, fmt.Errorf("ffmpeg not found — install ffmpeg to enable the camera (brew install ffmpeg)")
	}

	out, errText, err := s.runCapture(ctx, s.framerate)
	if err != nil && s.framerate == "" {
		// The device rejected whatever ffmpeg picked. It also printed the rates
		// it DOES accept, so ask again with one of those rather than guessing.
		if rate, ok := negotiateFramerate(errText); ok {
			s.framerate = rate
			out, errText, err = s.runCapture(ctx, rate)
		}
	}
	if err != nil {
		return Frame{}, fmt.Errorf("ffmpeg capture: %w: %s", err, describeCaptureFailure(ctx, errText))
	}
	if len(out) == 0 {
		return Frame{}, fmt.Errorf("ffmpeg produced no frame data")
	}

	return Frame{
		CapturedAt:   time.Now(),
		Data:         out,
		SourceDevice: s.Device,
	}, nil
}

// runCapture executes one ffmpeg invocation and returns stdout, stderr, error.
func (s *VisionCaptureService) runCapture(ctx context.Context, framerate string) ([]byte, string, error) {
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, s.bin, s.captureArgs(framerate)...)
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return out.Bytes(), strings.TrimSpace(errBuf.String()), err
}

// framerateRe finds the rates a device advertises in ffmpeg's own error output:
//
//	Supported modes:
//	  640x480@[15.000000 30.000000]fps
var framerateRe = regexp.MustCompile(`@\[([0-9. ]+)\]fps`)

// negotiateFramerate picks a capture rate the device actually supports, read
// from ffmpeg's rejection.
//
// This exists because a hardcoded rate is unfixable from the outside and fails
// closed. The darwin path used to pass "-framerate 1" — a reasonable-sounding
// way to ask a camera for as little work as possible, and one that no real
// webcam accepts. A MacBook Air camera advertises exactly 15 and 30 fps, so
// avfoundation refused the input outright:
//
//	Selected framerate (1.000000) is not supported by the device.
//	Error opening input: Input/output error
//
// Every capture on that hardware failed before a byte was read. Dropping the
// flag is not a fix either: ffmpeg then defaults to 29.97, which the same
// device also refuses. The only durable answer is to let the device say what
// it takes. (Pixel format needs no such handling — ffmpeg overrides an
// unsupported one to a supported one by itself.)
//
// Args: stderr from a failed capture.
// Returns: the chosen rate as an ffmpeg argument, and whether one was found.
// Complexity: O(len(stderr)).
func negotiateFramerate(stderr string) (string, bool) {
	if !strings.Contains(stderr, "not supported by the device") {
		return "", false
	}
	best := ""
	var bestVal float64
	for _, m := range framerateRe.FindAllStringSubmatch(stderr, -1) {
		for _, field := range strings.Fields(m[1]) {
			v, err := strconv.ParseFloat(field, 64)
			if err != nil || v <= 0 {
				continue
			}
			// Prefer the highest advertised rate: one frame is taken either
			// way, and a faster mode reaches that frame sooner.
			if v > bestVal {
				bestVal, best = v, field
			}
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// describeCaptureFailure adds the cause ffmpeg cannot report itself.
//
// A camera the OS has not authorized does not fail — it BLOCKS, silently, with
// no stderr at all, until the context deadline. That is indistinguishable from
// a hung device unless someone says so, and on macOS it is by far the likelier
// explanation.
func describeCaptureFailure(ctx context.Context, stderr string) string {
	if stderr != "" {
		return stderr
	}
	if ctx.Err() == nil {
		return "ffmpeg exited without output"
	}
	if runtime.GOOS == "darwin" {
		return "no frame and no error before the deadline — macOS is most likely " +
			"withholding camera access; grant it to your terminal in System Settings → " +
			"Privacy & Security → Camera, then restart the terminal"
	}
	return "no frame and no error before the deadline — the camera may be in use by another process"
}

// captureArgs assembles the ffmpeg invocation. The final argument is always
// "-" (image2pipe → stdout): there is never an on-disk output file.
func (s *VisionCaptureService) captureArgs(framerate string) []string {
	args := s.inputArgs(framerate)
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
func (s *VisionCaptureService) inputArgs(framerate string) []string {
	rate := []string{}
	if framerate != "" {
		rate = []string{"-framerate", framerate}
	}
	switch runtime.GOOS {
	case "darwin":
		dev := s.Device
		if dev == "" {
			dev = "default"
		}
		return append([]string{"-f", "avfoundation"}, append(rate, "-i", dev)...)
	case "windows":
		dev := s.Device
		if dev == "" {
			dev = "video=Integrated Camera"
		}
		return append([]string{"-f", "dshow"}, append(rate, "-i", dev)...)
	default:
		dev := s.Device
		if dev == "" {
			dev = "/dev/video0"
		}
		return append([]string{"-f", "v4l2"}, append(rate, "-i", dev)...)
	}
}
