// cmd/helix/blackbox_test.go
// Purpose: the contracts of live mode that are not about audio hardware — the
// folded command surface, the honesty of the readiness report, and the
// companion loop's two cost controls (frame diffing and the cooldown).
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

	"helix/internal/config"
)

// The eight folded verbs must be gone from the registry AND must land somewhere
// better than "unrecognized signal" — a command that worked yesterday is the
// worst thing to answer with a did-you-mean list.
func TestFoldedVoiceCommandsAreGoneButFindable(t *testing.T) {
	folded := []string{"/voice", "/manual", "/voice-setup", "/voice-status",
		"/wake", "/say", "/tts", "/eyes"}

	for _, name := range folded {
		if _, ok := lookupCommand(name); ok {
			t.Errorf("%s is still registered — it folded into /blackbox", name)
		}
		note, moved := blackBoxMigrationNote(name)
		if !moved {
			t.Errorf("%s has no migration note; a user typing it gets no direction", name)
			continue
		}
		if note == "" {
			t.Errorf("%s has an empty migration note", name)
		}
	}

	// And the replacement exists, with a handler and a voice route.
	cmd, ok := lookupCommand("/blackbox")
	if !ok {
		t.Fatal("/blackbox is not registered")
	}
	if cmd.Handler == nil {
		t.Error("/blackbox has no handler")
	}
	if !cmd.VoiceOK {
		t.Error("/blackbox must be reachable by voice — it is the voice mode")
	}
	if alias, ok := lookupCommand("/bb"); !ok || alias.Name != "/blackbox" {
		t.Error("/bb must alias /blackbox")
	}
}

// A command that never existed must not acquire a migration note.
func TestMigrationNoteOnlyCoversFoldedVerbs(t *testing.T) {
	for _, name := range []string{"/status", "/nonsense", "", "/blackbox"} {
		if _, moved := blackBoxMigrationNote(name); moved {
			t.Errorf("%q should not have a migration note", name)
		}
	}
}

func TestCmdArgsShift(t *testing.T) {
	c := cmdArgs{Name: "/blackbox", Raw: "/blackbox eyes on", Rest: "eyes on", Fields: []string{"eyes", "on"}}
	got := c.Shift()
	if got.Sub() != "on" {
		t.Errorf("Sub() = %q, want the subcommand's own argument", got.Sub())
	}
	if got.Rest != "on" {
		t.Errorf("Rest = %q, want %q", got.Rest, "on")
	}
	if got.Name != "/blackbox eyes" {
		t.Errorf("Name = %q, want the consumed verb appended", got.Name)
	}
	// Shifting an empty argument list must not panic or invent a field.
	empty := cmdArgs{Name: "/blackbox"}.Shift()
	if empty.Rest != "" || len(empty.Fields) != 0 {
		t.Errorf("shifting no arguments produced %+v", empty)
	}
	// A single argument leaves nothing behind.
	one := cmdArgs{Name: "/blackbox", Rest: "status", Fields: []string{"status"}}.Shift()
	if one.Rest != "" || one.Sub() != "" {
		t.Errorf("shifting one argument produced %+v", one)
	}
}

// The readiness report must not claim a camera that cannot produce a frame.
// This is the specific defect the old gate had: it asked only whether a MODEL
// could see, so a host with no ffmpeg reported ready and failed at the shutter.
func TestVisionReadyRequiresCaptureNotJustAModel(t *testing.T) {
	saved := visionSvc
	defer func() { visionSvc = saved }()

	visionSvc = nil // stands in for a host with no ffmpeg
	ready, why := visionReady()
	if ready {
		t.Fatal("vision must not report ready when no frame can be captured")
	}
	if why == "" {
		t.Fatal("an unready camera must say why")
	}
	if !bytes.Contains([]byte(why), []byte("ffmpeg")) {
		t.Errorf("reason = %q, want it to name the missing piece", why)
	}
}

func TestBlackBoxEyesLineReportsEnabledButBlind(t *testing.T) {
	saved, savedCfg := visionSvc, cfg
	defer func() { visionSvc, cfg = saved, savedCfg }()

	visionSvc = nil
	cfg = &config.Config{}
	cfg.Vision.Enabled = true

	// Enabled with no way to capture is the state the old report could not
	// express at all, and the one most worth saying out loud.
	if line := blackBoxEyesLine(); !bytes.Contains([]byte(line), []byte("blind")) {
		t.Errorf("eyes line = %q, want it to say the camera is on but unusable", line)
	}
}

// ---------------------------------------------------------------------------
// Companion: the two cost controls
// ---------------------------------------------------------------------------

// solidFrame builds a JPEG of one flat colour, plus an optional bright patch —
// enough to exercise the change detector without a camera or a fixture file.
func solidFrame(t *testing.T, lum uint8, patch bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{R: lum, G: lum, B: lum, A: 255})
		}
	}
	if patch {
		for y := 10; y < 90; y++ {
			for x := 10; x < 130; x++ {
				img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// The whole affordability argument rests on this: two frames of a motionless
// scene must compare as unchanged even though their JPEG bytes differ.
func TestFrameDeltaIgnoresEncodingNoiseAndCatchesRealChange(t *testing.T) {
	a, b := solidFrame(t, 90, false), solidFrame(t, 90, false)
	fpA, err := frameFingerprint(a)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	fpB, err := frameFingerprint(b)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if d := frameDelta(fpA, fpB); d >= config.CompanionDefaults().ChangeThreshold {
		t.Errorf("identical scenes differ by %.4f — the companion would spend a model call on nothing", d)
	}

	fpC, err := frameFingerprint(solidFrame(t, 90, true))
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if d := frameDelta(fpA, fpC); d < config.CompanionDefaults().ChangeThreshold {
		t.Errorf("a large bright patch moved the frame by only %.4f — real change would be missed", d)
	}
}

func TestFrameDeltaTreatsIncomparableFramesAsChanged(t *testing.T) {
	if d := frameDelta(nil, nil); d != 1 {
		t.Errorf("empty fingerprints delta = %v, want 1 (fail toward looking)", d)
	}
	if d := frameDelta([]float64{0.5}, []float64{0.1, 0.2}); d != 1 {
		t.Errorf("mismatched fingerprints delta = %v, want 1", d)
	}
}

func TestCompanionQueueRespectsSentinelAndCooldown(t *testing.T) {
	savedCfg := cfg
	defer func() { cfg = savedCfg }()
	cfg = &config.Config{Companion: config.CompanionDefaults()}

	c := &companionState{}

	// Silence is the default answer and must never become speech.
	for _, quiet := range []string{"NOTHING", "nothing", "  NOTHING.  ", ""} {
		c.queue(quiet)
		if c.pending != "" {
			t.Fatalf("the sentinel %q was queued as a remark: %q", quiet, c.pending)
		}
	}

	c.queue("Your coffee is about to spill.")
	if c.pending == "" {
		t.Fatal("a real remark must be queued")
	}

	// A remark just spoken starts the cooldown; the next one is dropped.
	c.pending = ""
	c.lastAt = time.Now()
	c.queue("Something else happened.")
	if c.pending != "" {
		t.Errorf("a remark landed inside the cooldown: %q", c.pending)
	}

	// Past the cooldown, Helix may speak again.
	c.lastAt = time.Now().Add(-2 * cfg.Companion.Cooldown())
	c.queue("The light changed.")
	if c.pending != "The light changed." {
		t.Errorf("pending = %q, want the new remark once the cooldown lapsed", c.pending)
	}

	// A newer observation replaces an older unspoken one: speaking about a
	// scene that has already moved on is worse than dropping it.
	c.queue("You picked up the mug.")
	if c.pending != "You picked up the mug." {
		t.Errorf("pending = %q, want the newest observation", c.pending)
	}
}

// A model that ignores "one short sentence" must not get to deliver a monologue
// through the speaker.
func TestCompanionTrimsToOneSentence(t *testing.T) {
	savedCfg := cfg
	defer func() { cfg = savedCfg }()
	cfg = &config.Config{Companion: config.CompanionDefaults()}

	c := &companionState{}
	c.queue("You look stuck. The stack trace on screen points at line 42. " +
		"You could also try rebuilding. And another thing.")
	if c.pending != "You look stuck." {
		t.Errorf("pending = %q, want only the first sentence", c.pending)
	}
}

func TestCompanionDefaultsAreEnabledAndBounded(t *testing.T) {
	d := config.CompanionDefaults()
	if !d.Enabled {
		t.Error("live mode's companion should be on by default — that is the feature")
	}
	if d.Interval() <= 0 || d.Cooldown() <= 0 {
		t.Fatal("interval and cooldown must be positive")
	}
	// Speaking more often than looking would queue remarks faster than the
	// scene can change.
	if d.Cooldown() < d.Interval() {
		t.Errorf("cooldown %s is shorter than the look interval %s", d.Cooldown(), d.Interval())
	}
	// A zero-valued config must resolve to the defaults, not to a hot loop.
	var zero config.CompanionConfig
	if zero.Interval() != d.Interval() || zero.Cooldown() != d.Cooldown() {
		t.Error("an unset companion config must fall back to the defaults")
	}
	if zero.Threshold() != d.ChangeThreshold {
		t.Error("an unset threshold must fall back to the default")
	}
}

// Pacing must back OFF when the machine is slow, not speed up when it is fast.
// A companion is bounded by how often a person wants to be spoken to; sampling
// harder buys nothing and takes the runtime away from the conversation.
func TestCompanionPacingBacksOffOnSlowLooks(t *testing.T) {
	savedCfg := cfg
	defer func() { cfg = savedCfg }()
	cfg = &config.Config{Companion: config.CompanionDefaults()}

	c := &companionState{}
	if got := c.lastLookDuration(); got != 0 {
		t.Fatalf("a fresh companion has no measurement, got %v", got)
	}

	// A look far slower than the interval must raise the observed pace...
	slow := 4 * cfg.Companion.Interval()
	c.recordLookDuration(slow)
	if c.lastLookDuration() != slow {
		t.Errorf("first measurement = %v, want it taken as-is (%v)", c.lastLookDuration(), slow)
	}

	// ...but one slow frame (a cold model load) must not pin the loop there.
	c.recordLookDuration(0)
	if got := c.lastLookDuration(); got != slow/2 {
		t.Errorf("smoothed = %v, want the average %v — one cold start should decay", got, slow/2)
	}

	// A fast machine never drives the pace below the configured interval:
	// that is the floor, and looking harder is not the goal.
	for i := 0; i < 10; i++ {
		c.recordLookDuration(time.Millisecond)
	}
	if c.lastLookDuration() >= cfg.Companion.Interval() {
		t.Errorf("fast looks should settle well under the interval, got %v", c.lastLookDuration())
	}
}

// The spoken safety valve must open for a sentence, not only for an incantation.
// QA said "Excellent. Now switch to manual mode." and Helix — which required an
// exact whole-transcript match — sent it to the planner, which replied by asking
// what to switch to manual mode FOR.
func TestKillPhraseMatchesNaturalSpeech(t *testing.T) {
	for _, said := range []string{
		"manual mode",
		"Manual mode.",
		"Excellent. Now switch to manual mode.",
		"okay let's switch to manual",
		"that's enough, stop listening",
		"blackbox off",
	} {
		if !isVoiceKillPhrase(said) {
			t.Errorf("%q should end live mode", said)
		}
	}

	// A question ABOUT the feature is not a request to use it: the phrase lands
	// mid-sentence there, which is why this is suffix- not substring-matched.
	for _, said := range []string{
		"how do I switch to manual mode again",
		"what does manual mode do",
		"remind me about manual mode later please",
		"run the tests",
		"",
	} {
		if isVoiceKillPhrase(said) {
			t.Errorf("%q must NOT end live mode", said)
		}
	}
}

// Same reasoning for the camera kill switch: a privacy control that responds to
// exactly one wording is not a control.
func TestEyesOffPhraseMatchesNaturalSpeech(t *testing.T) {
	for _, said := range []string{
		"turn off your eyes",
		"Okay, turn off your eyes.",
		"eyes off",
		"please close your eyes",
	} {
		if !isEyesOffPhrase(said) {
			t.Errorf("%q should close the camera", said)
		}
	}
	for _, said := range []string{"open your eyes", "run the tests", ""} {
		if isEyesOffPhrase(said) {
			t.Errorf("%q must not close the camera", said)
		}
	}

	// Unlike the mode kill phrase, this one is allowed to over-trigger. "How do
	// I turn off your eyes?" closes the camera, and that is the right way round
	// for a privacy control: the false positive turns something OFF, and the
	// user is one "/blackbox eyes on" from undoing it.
	if !isEyesOffPhrase("how do I turn off your eyes") {
		t.Error("a privacy switch should fail toward closing the camera")
	}
}
