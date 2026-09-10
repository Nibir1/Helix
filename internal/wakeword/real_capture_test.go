// internal/wakeword/real_capture_test.go
// Purpose: the test that was missing. Every other energy-detector test in this
// package was written against synthetic full-scale sine waves, and that is how
// an unreachable threshold survived a green suite: the shipped balanced
// threshold was normalized RMS 0.12, the fixtures were tones at amplitude
// 0.25-0.9 (RMS 0.18-0.64), and the corpus that was supposed to represent
// "room noise it must ignore" reached RMS 0.028 — six times LOUDER than
// speech-level sound actually measures on a MacBook Pro built-in mic.
// Hands-free wake could not fire on real hardware and nothing here noticed.
//
// So these fixtures are recordings, not arithmetic. Captured with the same
// binary and flags the shipped capture path uses —
//
//	rec -q -r 16000 -c 1 -b 16 -e signed-integer out.wav trim 0 1.5
//
// on a MacBook Pro built-in microphone at macOS input volume 38:
//
//	testdata/room_quiet_16k.wav     RMS 0.00114  (an ordinary quiet room)
//	testdata/speech_level_16k.wav   RMS 0.00569  (speech at ~40cm)
//
// Both numbers are two orders of magnitude below where the old constants
// lived, which is the whole point. Re-measure either fixture with
// `sox <file> -n stat` if these assertions ever look wrong.
package wakeword

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"helix/internal/speech"
)

// scanLog collects ScanReports across the goroutine boundary. OnScan is called
// on the scan loop's own goroutine, so a bare slice append here races with the
// test reading it — caught by -race, and worth a type rather than a comment
// because every test below wants the same thing.
type scanLog struct {
	mu   sync.Mutex
	rows []ScanReport
}

func (l *scanLog) add(r ScanReport) {
	l.mu.Lock()
	l.rows = append(l.rows, r)
	l.mu.Unlock()
}

func (l *scanLog) snapshot() []ScanReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ScanReport(nil), l.rows...)
}

// realChunk loads one recorded fixture as the scanner would deliver it.
func realChunk(t *testing.T, name string) speech.AudioFormat {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return speech.AudioFormat{Kind: speech.KindWAV, SampleRate: 16000, Channels: 1, Bytes: data}
}

// TestRealMicrophoneLevelsAreWhereWeThinkTheyAre pins the measurement the rest
// of this file reasons from. If a fixture is ever replaced, this fails first
// and says so, instead of the detector tests failing for an unrelated-looking
// reason.
func TestRealMicrophoneLevelsAreWhereWeThinkTheyAre(t *testing.T) {
	room, err := RMS(realChunk(t, "room_quiet_16k.wav"))
	if err != nil {
		t.Fatalf("room fixture: %v", err)
	}
	voice, err := RMS(realChunk(t, "speech_level_16k.wav"))
	if err != nil {
		t.Fatalf("speech fixture: %v", err)
	}
	t.Logf("recorded on a MacBook Pro built-in mic at input volume 38:")
	t.Logf("  quiet room:    RMS %.5f", room)
	t.Logf("  speech at 40cm: RMS %.5f  (%.1fx the room floor)", voice, voice/room)

	if room > 0.005 {
		t.Errorf("room fixture RMS %.5f is too loud to stand for a quiet room", room)
	}
	if voice <= room*2 {
		t.Errorf("speech fixture RMS %.5f must sit clearly above the room floor %.5f",
			voice, room)
	}
	// The number that was wrong. Kept as an assertion so nobody reintroduces a
	// constant in that range without this test explaining why it cannot work.
	if voice >= 0.07 {
		t.Errorf("speech fixture RMS %.5f reaches the old absolute thresholds "+
			"(loose 0.07 / balanced 0.12); the fixture is no longer representative "+
			"of a built-in microphone", voice)
	}
}

// TestEnergyDetectorWakesOnRealSpeechAfterMeasuringTheRoom is the regression
// for the reported bug: `/blackbox wake on` said "listening" and speaking did
// nothing, forever.
func TestEnergyDetectorWakesOnRealSpeechAfterMeasuringTheRoom(t *testing.T) {
	room := realChunk(t, "room_quiet_16k.wav")
	voice := realChunk(t, "speech_level_16k.wav")

	// The speech fixture is a WEAK proxy on purpose — macOS `say` through the
	// laptop's own speakers, not a person addressing the machine — so it sits
	// near the bottom edge of what counts as speech (RMS 0.0057, against
	// 0.012-0.033 measured for a clearly audible voice on the same mic). Every
	// preset has to clear even this one, which is the margin that makes a real
	// voice safe.
	for _, preset := range []Preset{PresetStrict, PresetBalanced, PresetLoose} {
		d := NewEnergyDetector(preset)

		// Standby: the room, repeatedly. None of it may wake anything.
		for i := 0; i < 6; i++ {
			score, woke, err := d.Wake(room)
			if err != nil {
				t.Fatalf("%s: room chunk %d: %v", preset, i, err)
			}
			if woke {
				t.Fatalf("%s: quiet room woke the detector on chunk %d "+
					"(rms %.5f vs bar %.5f) — that is an unwanted turn",
					preset, i, score, d.Bar())
			}
		}

		// Someone speaks.
		score, woke, err := d.Wake(voice)
		if err != nil {
			t.Fatalf("%s: speech chunk: %v", preset, err)
		}
		t.Logf("%s: floor %.5f  bar %.5f  speech %.5f  woke=%v",
			preset, d.Floor(), d.Bar(), score, woke)
		if !woke {
			t.Errorf("%s: real speech at %.5f RMS did not wake a detector whose "+
				"bar is %.5f (measured room floor %.5f) — this is the shipped bug: "+
				"the prompt says listening and speaking does nothing",
				preset, score, d.Bar(), d.Floor())
		}
		// Whatever the verdict, the bar must be within reach of real speech.
		// The bug was a bar 25x above anything the device can produce.
		if d.Bar() > score*2 {
			t.Errorf("%s: bar %.5f is more than double real speech at %.5f — "+
				"unreachable on this microphone, which is the class of bug this "+
				"file exists to catch", preset, d.Bar(), score)
		}
	}
}

// TestWakeServiceFiresOnRealSpeechThroughTheScanLoop runs the actual service
// loop — scanner to detector to event channel, with the ScanReport hook the
// diagnostics use — over recorded audio. The detector-level test above can pass
// while the loop still swallows the result, which is exactly what happened.
func TestWakeServiceFiresOnRealSpeechThroughTheScanLoop(t *testing.T) {
	room := realChunk(t, "room_quiet_16k.wav")
	voice := realChunk(t, "speech_level_16k.wav")

	chunks := []speech.AudioFormat{room, room, room, voice}
	// Padding so the loop does not run out of fixtures and end on a scanner
	// error before the event is delivered.
	for i := 0; i < 20; i++ {
		chunks = append(chunks, room)
	}

	log := &scanLog{}
	svc, err := NewService(&fixtureScanner{chunks: chunks},
		NewEnergyDetector(PresetBalanced),
		Config{
			Phrase:   "hey helix",
			Cooldown: 10 * time.Millisecond,
			OnScan:   log.add,
		})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatalf("the scan loop ended without waking: %v (reports: %+v)",
				svc.Err(), log.snapshot())
		}
		if ev.Score <= 0 {
			t.Errorf("wake event carries no level: %+v", ev)
		}
		t.Logf("woke at level %.5f after %d scanned chunks",
			ev.Score, len(log.snapshot()))
	case <-ctx.Done():
		t.Fatalf("real speech never produced a wake event; reports: %+v",
			log.snapshot())
	}
}

// TestScanReportCarriesTheNumbersADiagnosisNeeds pins the instrumentation
// contract. The reported bug was undiagnosable from the outside because the
// loop emitted nothing per chunk and discarded every error, so "it reports the
// level and the bar it compared against" is a behaviour, not a nicety.
func TestScanReportCarriesTheNumbersADiagnosisNeeds(t *testing.T) {
	room := realChunk(t, "room_quiet_16k.wav")

	log := &scanLog{}
	svc, err := NewService(&fixtureScanner{chunks: []speech.AudioFormat{room, room, room}},
		NewEnergyDetector(PresetBalanced),
		Config{OnScan: log.add})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-events // fixtures exhaust, the scanner errors out, the loop ends
	_ = svc.Stop()

	var scored, failed int
	for _, r := range log.snapshot() {
		if r.Err != nil {
			failed++
			continue
		}
		scored++
		if r.RMS <= 0 {
			t.Errorf("scan report has no level: %+v", r)
		}
		if r.Threshold <= 0 {
			t.Errorf("scan report does not say what the chunk was compared "+
				"against, so a mis-set bar is invisible: %+v", r)
		}
	}
	if scored == 0 {
		t.Error("no chunk was reported — the wake loop is unobservable again")
	}
	if failed == 0 {
		t.Error("the exhausted scanner's failures were not reported, which is how " +
			"a dying wake loop stayed silent")
	}
	if svc.Err() == nil {
		t.Error("the loop ended on persistent capture failure but Err() is nil, " +
			"so a caller cannot tell a dead listener from a quiet room")
	}
}
