// cmd/helix/startup_order_test.go
// Purpose: two startup steps whose ORDER is a correctness property, pinned
// because nothing else in the package can express "this must run after that".
package main

import (
	"os"
	"strings"
	"testing"
)

// The ordering is load-bearing: initVoiceMode prints the live banner for a
// restored session, and that banner reports the camera. Called before visionSvc
// exists, it blamed a missing ffmpeg for a service that had not been built.
func TestInitVoiceModeRunsAfterTheVisionService(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	vision := strings.Index(s, "visionSvc = vision.NewCaptureService()")
	init := strings.Index(s, "\n\tinitVoiceMode()")
	if vision < 0 || init < 0 {
		t.Fatalf("anchors not found (vision=%d init=%d)", vision, init)
	}
	if init < vision {
		t.Error("initVoiceMode() runs before the camera service exists; a restored " +
			"live session will report the camera as broken and blame ffmpeg")
	}
}
