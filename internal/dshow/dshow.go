// internal/dshow/dshow.go
// Purpose: find a real capture device on Windows instead of guessing its name.
//
// THE BUG THIS EXISTS FOR, TWICE. Windows capture was wired with hardcoded
// device names — `audio=Microphone` for the microphone and
// `video=Integrated Camera` for the camera — and neither is a device type in
// DirectShow. They are literal friendly names, and almost no machine has either
// one: real hardware reports "Microphone Array (Realtek(R) Audio)" or
// "HD Webcam C920". ffmpeg could not find the device, exited -1, and Helix
// reported
//
//	capture: ffmpeg recording failed: exit status 0xffffffff
//	✘ voice unavailable
//
// on a machine whose microphone was working and whose ffmpeg was installed.
//
// It is one package rather than one copy per capture path because it was one
// bug written twice: the microphone was reported, the camera was found while
// fixing it, and a second copy is a second thing to fix the next time.
//
// So ask ffmpeg what is actually there. The listing format was read out of
// ffmpeg's own libavdevice/dshow.c rather than remembered, because it changed:
//
//	current (>= 5.x), one line per device, type in the suffix —
//	    [dshow @ 0000...] "Microphone Array (Realtek(R) Audio)" (audio)
//	    [dshow @ 0000...]   Alternative name "@device_cm_{...}\wave_{...}"
//
//	older (<= 4.4), section headers, no suffix —
//	    [dshow @ 0000...] DirectShow video devices (some may be both video and audio devices)
//	    [dshow @ 0000...]  "Integrated Camera"
//	    [dshow @ 0000...]     Alternative name "@device_pnp_\\?\usb#..."
//	    [dshow @ 0000...] DirectShow audio devices
//	    [dshow @ 0000...]  "Microphone Array (Realtek(R) Audio)"
//
// Both are parsed, because which one a user has depends on where their ffmpeg
// came from and MSYS2, gyan.dev and winget do not ship the same build.
package dshow

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Kind selects which half of the listing to read.
type Kind int

const (
	Audio Kind = iota
	Video
)

// listTimeout caps the enumeration. It talks to DirectShow, which can block on
// a wedged driver, and a device that cannot be listed in five seconds is not
// one this turn is going to use.
const listTimeout = 5 * time.Second

var (
	once  [2]sync.Once
	cache [2][]string
)

// Devices returns the DirectShow capture devices of a kind, most preferred
// first. Enumeration costs a process spawn, so it happens once per run per
// kind — hardware appearing mid-session is rarer than a turn being taken.
func Devices(kind Kind) []string {
	once[kind].Do(func() { cache[kind] = list(kind) })
	return cache[kind]
}

func list(kind Kind) []string {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()

	// NOT -loglevel error. The listing is printed at INFO, so quieting ffmpeg
	// the way the capture commands do would silence the very output being
	// asked for. It all goes to stderr, and the command always exits non-zero
	// ("Error opening input file dummy") even when the listing is perfect —
	// so the exit code is deliberately ignored and only the text is read.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-list_devices", "true",
		"-f", "dshow", "-i", "dummy")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()

	return ParseDevices(errBuf.String(), kind)
}

var (
	quoted   = regexp.MustCompile(`"([^"]+)"`)
	audioHdr = regexp.MustCompile(`(?i)DirectShow audio devices`)
	videoHdr = regexp.MustCompile(`(?i)DirectShow video devices`)
)

// ParseDevices extracts friendly names of the given kind from the output of
// `ffmpeg -list_devices true -f dshow -i dummy`.
func ParseDevices(out string, kind Kind) []string {
	var found []string
	seen := map[string]bool{}
	section := Kind(-1)

	for _, line := range strings.Split(out, "\n") {
		switch {
		case audioHdr.MatchString(line):
			section = Audio
			continue
		case videoHdr.MatchString(line):
			section = Video
			continue
		}

		// "Alternative name" lines quote a device path, not a name to pass on
		// the -i line, and matching them would double every device.
		if strings.Contains(line, "Alternative name") {
			continue
		}

		m := quoted.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]

		// The modern format states the type per device; trust it over the
		// section, since master emits no headers at all. A device that says
		// (video) is not a microphone even if it appeared after an audio
		// header, and "(video, audio)" — a webcam with a mic — is both.
		want := "audio"
		if kind == Video {
			want = "video"
		}
		hasType := strings.Contains(line, "(audio") || strings.Contains(line, "(video") ||
			strings.Contains(line, "(none)")
		isKind := section == kind
		if hasType {
			isKind = strings.Contains(line, "("+want) || strings.Contains(line, ", "+want+")")
		}
		if !isKind || seen[name] {
			continue
		}
		seen[name] = true
		found = append(found, name)
	}
	return found
}
