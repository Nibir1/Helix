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
	"sync"
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

	// mu guards the fields below, which a status report reads while the
	// companion loop writes them from its own goroutine.
	mu sync.Mutex

	// lastErr is why the most recent capture failed, and lastOK whether any
	// capture has ever succeeded.
	//
	// These exist so a status report can stop overstating. ffmpeg on PATH and a
	// multimodal model are both necessary and neither is sufficient: a camera the
	// OS has not authorized satisfies both checks and still delivers nothing, so
	// /blackbox status said "watching" on a machine where no frame could ever
	// arrive. Detecting that up front would mean an 8-second capture attempt per
	// status call, so instead the service remembers what actually happened.
	lastErr error
	lastOK  bool
}

// LastFailure reports the most recent capture error, and whether any capture has
// ever succeeded. A nil error with ok=false means "never attempted".
func (s *VisionCaptureService) LastFailure() (err error, everWorked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr, s.lastOK
}

// recordOutcome remembers the result of one capture.
func (s *VisionCaptureService) recordOutcome(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err
	if err == nil {
		s.lastOK = true
	}
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

// CaptureDeadline bounds one ffmpeg capture attempt.
//
// Generous against reality and short against patience: opening an avfoundation
// or v4l2 device plus decoding one frame is well under a second on working
// hardware, and a cold USB webcam a couple of seconds at worst. Eight seconds is
// several times that, while being short enough that an unauthorized camera
// reports its cause promptly instead of stalling a turn. The negotiate path may
// spend this twice (the first attempt fails fast on a rejected framerate).
const CaptureDeadline = 8 * time.Second

// CaptureFrame grabs one frame and returns it as in-memory bytes.
//
// The capture rate is NEGOTIATED, not assumed — the same lesson the provider
// adapters learned about max_tokens. See negotiateFramerate for what a guessed
// value costs.
func (s *VisionCaptureService) CaptureFrame(ctx context.Context) (Frame, error) {
	if !s.Available() {
		return Frame{}, fmt.Errorf("ffmpeg not found — install ffmpeg to enable the camera (brew install ffmpeg)")
	}

	// Bound the CAPTURE step on its own, rather than inheriting whatever budget
	// the caller allowed for the whole turn.
	//
	// Both call sites pass 30s, which is right for a turn and far too long for a
	// device open: a working camera yields a frame in well under a second, and a
	// camera the OS has not authorized yields nothing ever. Measured on a real
	// denied camera, the old behavior was thirty seconds of silence — which is
	// precisely the "looks like a hang" this path was supposed to have stopped
	// looking like. A shorter, capture-specific deadline is what makes the
	// failure legible; the caller's context still wins if it is sooner.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > CaptureDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, CaptureDeadline)
		defer cancel()
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
		failure := fmt.Errorf("ffmpeg capture: %w: %s", err, describeCaptureFailure(ctx, errText))
		s.recordOutcome(failure)
		return Frame{}, failure
	}
	if len(out) == 0 {
		failure := fmt.Errorf("ffmpeg produced no frame data")
		s.recordOutcome(failure)
		return Frame{}, failure
	}

	s.recordOutcome(nil)
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
// no stderr of its own, until the context deadline. That is indistinguishable
// from a hung device unless someone says so, and on macOS it is by far the
// likelier explanation.
//
// "Whatever ffmpeg printed" is NOT the same as "why the capture failed", and
// telling them apart took two measurements against a real denied camera.
//
// First run: the user waited thirty seconds and was told
// `signal: killed: ffmpeg version 9.0.1 Copyright (c) 2000-2026…` — the version
// banner, which ffmpeg writes on every run, so "stderr is present" was always
// true and the guidance below was unreachable. -hide_banner (see inputArgs) fixed
// that. Second run, banner suppressed: stderr instead held
// `Selected pixel format (yuv420p) is not supported by the input device` — a
// NON-FATAL warning ffmpeg self-corrects from and then keeps going. It masked the
// cause just as effectively.
//
// So the rule is not an ordering between the two, it is which one is load-bearing.
// If the deadline expired, the expiry is the cause and anything ffmpeg said is
// supporting detail — worth keeping, not worth leading with. If ffmpeg exited on
// its own, its own words are the cause.
func describeCaptureFailure(ctx context.Context, stderr string) string {
	if ctx.Err() == nil {
		if stderr != "" {
			return stderr
		}
		return "ffmpeg exited without output"
	}

	cause := "no frame before the deadline — the camera may be in use by another process"
	if runtime.GOOS == "darwin" {
		cause = "no frame before the deadline — macOS is most likely " +
			"withholding camera access; grant it to your terminal in System Settings → " +
			"Privacy & Security → Camera, then restart the terminal"
	}
	if stderr != "" {
		return cause + " (ffmpeg also said: " + stderr + ")"
	}
	return cause
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
//
// -hide_banner is not cosmetic: without it ffmpeg writes its version and build
// configuration to stderr on every run, which made every capture look like it
// had produced diagnostic output and hid the real cause behind a copyright
// notice. Errors — including the "Supported modes:" list negotiateFramerate
// parses — are unaffected.
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
		return append([]string{"-hide_banner", "-f", "avfoundation"}, append(rate, "-i", dev)...)
	case "windows":
		dev := s.Device
		if dev == "" {
			dev = "video=Integrated Camera"
		}
		return append([]string{"-hide_banner", "-f", "dshow"}, append(rate, "-i", dev)...)
	default:
		dev := s.Device
		if dev == "" {
			dev = "/dev/video0"
		}
		return append([]string{"-hide_banner", "-f", "v4l2"}, append(rate, "-i", dev)...)
	}
}
