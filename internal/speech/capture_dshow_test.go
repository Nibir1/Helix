// internal/speech/capture_dshow_test.go
// Purpose: the DirectShow listing cannot be produced on this machine, so the
// parser is tested against the two formats ffmpeg's own source prints.
//
// The fixtures below are not invented. They are the exact shapes emitted by
// libavdevice/dshow.c:
//
//	master:  av_log(avctx, AV_LOG_INFO, "\"%s\"", friendly_name); ... " (%s"
//	         av_log(avctx, AV_LOG_INFO, "  Alternative name \"%s\"\n", unique_name);
//	n4.4:    av_log(avctx, AV_LOG_INFO, "DirectShow audio devices\n");
//	         av_log(avctx, AV_LOG_INFO, " \"%s\"\n", friendly_name);
//	         av_log(avctx, AV_LOG_INFO, "    Alternative name \"%s\"\n", unique_name);
package speech

import (
	"reflect"
	"strings"
	"testing"
)

// ffmpeg >= 5.x: no section headers at all, the type is a suffix on the line.
const dshowModernListing = `[dshow @ 000001f2a1b2c3d0] "Integrated Camera" (video)
[dshow @ 000001f2a1b2c3d0]   Alternative name "@device_pnp_\\?\usb#vid_04f2&pid_b6d9&mi_00#6&1e3c0a1&0&0000#{65e8773d-8f56-11d0-a3b9-00a0c9223196}\global"
[dshow @ 000001f2a1b2c3d0] "Microphone Array (Realtek(R) Audio)" (audio)
[dshow @ 000001f2a1b2c3d0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{B1F1A5C0-1111-2222-3333-444455556666}"
[dshow @ 000001f2a1b2c3d0] "HD Webcam C920" (video, audio)
[dshow @ 000001f2a1b2c3d0]   Alternative name "@device_pnp_\\?\usb#vid_046d&pid_082d#global"
[in#0 @ 000001f2a1b0f000] Error opening input: I/O error
Error opening input file dummy.
`

// ffmpeg <= 4.4: section headers, no per-device type.
const dshowLegacyListing = `[dshow @ 0000021d8f1c2400] DirectShow video devices (some may be both video and audio devices)
[dshow @ 0000021d8f1c2400]  "Integrated Camera"
[dshow @ 0000021d8f1c2400]     Alternative name "@device_pnp_\\?\usb#vid_04f2&pid_b6d9#global"
[dshow @ 0000021d8f1c2400] DirectShow audio devices
[dshow @ 0000021d8f1c2400]  "Microphone Array (Realtek(R) Audio)"
[dshow @ 0000021d8f1c2400]     Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{AAAA}"
[dshow @ 0000021d8f1c2400]  "Microphone (2- USB Audio Device)"
[dshow @ 0000021d8f1c2400]     Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{BBBB}"
dummy: Immediate exit requested
`

func TestParseDshowAudioDevices(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{
			// The camera must not be offered as a microphone, and the webcam
			// that is BOTH must be — "(video, audio)" is an audio device.
			name: "modern format, type suffix per device",
			out:  dshowModernListing,
			want: []string{"Microphone Array (Realtek(R) Audio)", "HD Webcam C920"},
		},
		{
			// No suffixes here, so the section header is the only thing
			// separating a camera from a microphone.
			name: "legacy format, section headers",
			out:  dshowLegacyListing,
			want: []string{"Microphone Array (Realtek(R) Audio)", "Microphone (2- USB Audio Device)"},
		},
		{
			name: "no devices at all",
			out:  "[dshow @ 0000] DirectShow video devices\n[dshow @ 0000] DirectShow audio devices\n",
			want: nil,
		},
		{
			name: "not a listing at all",
			out:  "ffmpeg: command not found",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDshowAudioDevices(tc.out)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseDshowAudioDevices()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// The alternative-name lines are quoted too. Matching them would return
// "@device_cm_{...}" as if it were a friendly name, and every device would
// appear twice — with the wrong one first.
func TestParseDshowIgnoresAlternativeNames(t *testing.T) {
	for _, got := range parseDshowAudioDevices(dshowModernListing) {
		if strings.HasPrefix(got, "@device") {
			t.Errorf("parser returned an alternative name as a device: %q", got)
		}
	}
}

// The name that broke it. "Microphone" is not a DirectShow device type, and a
// device called exactly that is not what real hardware reports — so a parser
// result must be preferred over the guess whenever there is one.
func TestParsedNameIsNotTheOldGuess(t *testing.T) {
	got := parseDshowAudioDevices(dshowLegacyListing)
	if len(got) == 0 {
		t.Fatal("parsed nothing from a listing with two microphones in it")
	}
	if got[0] == "Microphone" {
		t.Error("parser produced the bare guess that ffmpeg could never resolve")
	}
	if !strings.Contains(got[0], "Realtek") {
		t.Errorf("first device is %q; the listing's first audio device is the Realtek array", got[0])
	}
}
