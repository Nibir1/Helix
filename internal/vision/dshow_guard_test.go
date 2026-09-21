package vision

import (
	"os"
	"strings"
	"testing"
)

// The camera must not be addressed by a guessed DirectShow friendly name — the
// same defect that made the microphone unusable on Windows.
func TestWindowsCameraIsEnumeratedNotGuessed(t *testing.T) {
	src, err := os.ReadFile("capture.go")
	if err != nil {
		t.Fatalf("read capture.go: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "dshow.Devices(dshow.Video)") {
		t.Error("the Windows camera device is not enumerated; a hardcoded friendly " +
			"name fails on every machine whose webcam is named anything else")
	}
}
