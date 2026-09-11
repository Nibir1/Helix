// internal/live/opus_test.go
// Purpose: the real codec, against the real library — and a LOUD skip when it
// is absent (§9 rule 6), never a silent pass.
//
// Nothing else in the suite needs libopus: session_test.go injects a fake. This
// file is the one place the purego binding itself is exercised, so if it is
// skipped the binding is untested and the skip has to say so.
package live

import "testing"

func TestOpusRoundTrip(t *testing.T) {
	if err := OpusAvailable(); err != nil {
		t.Skipf("libopus is not installed, so the purego binding is NOT covered by this run: %v", err)
	}

	enc, err := NewEncoder(AudioSampleRate, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	dec, err := NewDecoder(AudioSampleRate, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	// A 440 Hz tone, which is signal rather than silence: an encoder that is
	// wired up wrongly still produces plausible-looking bytes for silence.
	pcm := make([]int16, FrameSamples)
	for i := range pcm {
		pcm[i] = int16(8000 * sinApprox(float64(i)*2*3.14159265*440/AudioSampleRate))
	}

	packet, err := enc.Encode(pcm, FrameSamples)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode produced no bytes")
	}
	if len(packet) >= len(pcm)*2 {
		t.Errorf("Encode produced %d bytes for %d raw — it is not compressing, and a frame "+
			"that large exceeds the receive MTU", len(packet), len(pcm)*2)
	}

	out, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != FrameSamples {
		t.Errorf("Decode returned %d samples, want %d", len(out), FrameSamples)
	}
	// Opus is lossy, so the assertion is on ENERGY: a decode that returns
	// silence for a tone is the failure that matters, and comparing samples
	// would fail on a correct codec.
	var energy float64
	for _, s := range out {
		energy += float64(s) * float64(s)
	}
	if energy/float64(len(out)) < 1000 {
		t.Error("the decoded frame is effectively silent; the round trip lost the signal")
	}
}

func TestEncodeRejectsAShortFrame(t *testing.T) {
	if err := OpusAvailable(); err != nil {
		t.Skipf("libopus is not installed, so the purego binding is NOT covered by this run: %v", err)
	}
	enc, err := NewEncoder(AudioSampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, err := enc.Encode(make([]int16, 10), FrameSamples); err == nil {
		t.Error("Encode accepted a frame shorter than frameSize; libopus would read past the slice")
	}
}

func TestClosedCodecIsSafe(t *testing.T) {
	if err := OpusAvailable(); err != nil {
		t.Skipf("libopus is not installed, so the purego binding is NOT covered by this run: %v", err)
	}
	enc, _ := NewEncoder(AudioSampleRate, 1)
	enc.Close()
	enc.Close() // must not double-free
	if _, err := enc.Encode(make([]int16, FrameSamples), FrameSamples); err == nil {
		t.Error("a closed encoder still encoded; that is a use-after-free into C")
	}
	dec, _ := NewDecoder(AudioSampleRate, 1)
	dec.Close()
	dec.Close()
	if _, err := dec.Decode([]byte{1, 2, 3}); err == nil {
		t.Error("a closed decoder still decoded")
	}
}

// sinApprox is a small Taylor/quadrant sine so the test needs no import for one
// call. Accuracy is irrelevant — it only has to be non-constant.
func sinApprox(x float64) float64 {
	const twoPi = 6.283185307179586
	for x > twoPi {
		x -= twoPi
	}
	// Bhaskara I's approximation, mirrored for the second half.
	if x > 3.141592653589793 {
		return -sinApprox(x - 3.141592653589793)
	}
	return 16 * x * (3.141592653589793 - x) /
		(49.348022005446789 - 4*x*(3.141592653589793-x))
}
