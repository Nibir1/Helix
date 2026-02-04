package audio

import (
	"math"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// SampleRate defines CD-quality audio
const SampleRate = 44100

var initialized bool

// Init starts the audio speaker. Call this once at startup.
func Init() error {
	sr := beep.SampleRate(SampleRate)
	// We use a slightly smaller buffer (50ms) for tighter response time
	err := speaker.Init(sr, sr.N(time.Second/20))
	if err == nil {
		initialized = true
	}
	return err
}

// ---------------------------------------------------------
// SOUND GENERATORS (SYNTHESIZERS)
// ---------------------------------------------------------

// PlayClick generates a clean "Sci-Fi Data Tap" sound
// (Replaces the "cockroach" white noise with a tight Sine blip)
func PlayClick() {
	if !initialized {
		return
	}

	// 400Hz = Moderate "Data" pitch.
	// 150Hz = "Thock" (Mechanical)
	// 800Hz = "Bip" (High tech)
	// Let's go with 350Hz for a solid "Terminal" sound.
	sine := &SineWave{Freq: 350, Phase: 0}

	// Very short duration: 25ms
	duration := beep.Take(SampleRate/40, sine)

	start := time.Now()

	// Envelope: Sharp attack, fast decay
	click := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = duration.Stream(samples)
		for i := range samples {
			// Linear decay over the short duration
			elapsed := time.Since(start).Seconds()
			// Decay faster (multiplier 40) to make it percussive
			vol := math.Max(0, 1.0-(elapsed*40))

			// Volume: 15% (Subtle)
			samples[i][0] *= (0.15 * vol)
			samples[i][1] *= (0.15 * vol)
		}
		return n, ok
	})

	speaker.Play(click)
}

// PlayAlert generates a high-tech sine wave ping (for Modals)
func PlayAlert() {
	if !initialized {
		return
	}
	// 880Hz Sine Wave (High A) - Sharp attention grabber
	sine := &SineWave{Freq: 880, Phase: 0}

	// Play for 100ms
	beepSound := beep.Take(SampleRate/10, sine)

	// Add a slight decay so it doesn't "pop" at the end
	start := time.Now()
	alert := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = beepSound.Stream(samples)
		for i := range samples {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*5)) // Slow decay
			samples[i][0] *= (0.3 * vol)        // Louder than click
			samples[i][1] *= (0.3 * vol)
		}
		return n, ok
	})

	speaker.Play(alert)
}

// PlayError generates a low buzz (Sawtooth-ish)
func PlayError() {
	if !initialized {
		return
	}
	// 110Hz Low buzz
	saw := &SawWave{Freq: 110}

	// 200ms duration
	buzz := beep.Take(SampleRate/5, saw)

	start := time.Now()
	errorSound := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = buzz.Stream(samples)
		for i := range samples {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*4))
			samples[i][0] *= (0.2 * vol)
			samples[i][1] *= (0.2 * vol)
		}
		return n, ok
	})

	speaker.Play(errorSound)
}

// ---------------------------------------------------------
// WAVEFORM IMPLEMENTATIONS
// ---------------------------------------------------------

// Sine Wave Generator (Smoother sound)
type SineWave struct {
	Freq  float64
	Phase float64
}

func (s *SineWave) Stream(samples [][2]float64) (int, bool) {
	step := s.Freq * 2 * math.Pi / float64(SampleRate)
	for i := range samples {
		v := math.Sin(s.Phase)
		samples[i][0] = v
		samples[i][1] = v
		s.Phase += step
	}
	return len(samples), true
}
func (s *SineWave) Err() error { return nil }

// Sawtooth Generator (Rougher sound for errors)
type SawWave struct {
	Freq  float64
	Phase float64
}

func (s *SawWave) Stream(samples [][2]float64) (int, bool) {
	step := s.Freq / float64(SampleRate)
	for i := range samples {
		v := (s.Phase * 2.0) - 1.0 // Map 0..1 to -1..1
		samples[i][0] = v
		samples[i][1] = v
		s.Phase += step
		if s.Phase > 1.0 {
			s.Phase -= 1.0
		}
	}
	return len(samples), true
}
func (s *SawWave) Err() error { return nil }
