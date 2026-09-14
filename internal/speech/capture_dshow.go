// internal/speech/capture_dshow.go
// Purpose: find a real microphone on Windows instead of guessing its name.
//
// THE BUG THIS EXISTS FOR. The Windows capture command was
//
//	ffmpeg -f dshow -i "audio=Microphone"
//
// and "Microphone" is not a device type in DirectShow — it is a literal
// friendly name, and almost no machine has a device called exactly that. Real
// ones read "Microphone Array (Realtek(R) Audio)" or "Microphone (2- USB Audio
// Device)". ffmpeg could not find the device, exited -1, and Helix reported
//
//	capture: ffmpeg recording failed: exit status 0xffffffff
//	✘ voice unavailable
//
// on a machine whose microphone was working and whose ffmpeg was installed.
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
package speech

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// dshowListTimeout caps the enumeration. It talks to DirectShow, which can
// block on a wedged driver, and a microphone that cannot be listed in five
// seconds is not one this turn is going to use.
const dshowListTimeout = 5 * time.Second

var (
	dshowOnce  sync.Once
	dshowCache []string
)

// dshowAudioDevices returns the DirectShow audio capture devices, most
// preferred first. Enumeration costs a process spawn, so it happens once per
// run — a microphone appearing mid-session is rarer than a turn being taken.
func dshowAudioDevices() []string {
	dshowOnce.Do(func() { dshowCache = listDshowAudioDevices() })
	return dshowCache
}

func listDshowAudioDevices() []string {
	ctx, cancel := context.WithTimeout(context.Background(), dshowListTimeout)
	defer cancel()

	// NOT -loglevel error. The listing is printed at INFO, so quieting ffmpeg
	// the way the recording command does would silence the very output being
	// asked for. It all goes to stderr, and the command always exits non-zero
	// ("Error opening input file dummy") even when the listing is perfect —
	// so the exit code is deliberately ignored and only the text is read.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-list_devices", "true",
		"-f", "dshow", "-i", "dummy")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()

	return parseDshowAudioDevices(errBuf.String())
}

var (
	dshowQuoted   = regexp.MustCompile(`"([^"]+)"`)
	dshowAudioHdr = regexp.MustCompile(`(?i)DirectShow audio devices`)
	dshowVideoHdr = regexp.MustCompile(`(?i)DirectShow video devices`)
)

// parseDshowAudioDevices extracts audio-capable friendly names from the output
// of `ffmpeg -list_devices true -f dshow -i dummy`.
func parseDshowAudioDevices(out string) []string {
	var found []string
	seen := map[string]bool{}
	inAudioSection := false

	for _, line := range strings.Split(out, "\n") {
		switch {
		case dshowAudioHdr.MatchString(line):
			inAudioSection = true
			continue
		case dshowVideoHdr.MatchString(line):
			inAudioSection = false
			continue
		}

		// "Alternative name" lines quote a device path, not a name to pass on
		// the -i line, and matching them would double every device.
		if strings.Contains(line, "Alternative name") {
			continue
		}

		m := dshowQuoted.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]

		// The modern format states the type per device; trust it over the
		// section, since master emits no headers at all. A device that says
		// (video) is not a microphone even if it appeared after an audio
		// header, and "(video, audio)" — a webcam with a mic — is.
		hasType := strings.Contains(line, "(audio") || strings.Contains(line, "(video") ||
			strings.Contains(line, "(none)")
		isAudio := inAudioSection
		if hasType {
			isAudio = strings.Contains(line, "(audio") || strings.Contains(line, ", audio)")
		}
		if !isAudio || seen[name] {
			continue
		}
		seen[name] = true
		found = append(found, name)
	}
	return found
}
