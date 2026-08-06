// internal/audio/audio.go
package audio

import (
	"math"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

const SampleRate = 44100

var (
	initialized bool
	enabled     = true
)

func SetEnabled(on bool) { enabled = on }
func IsEnabled() bool    { return enabled }

func Init() error {
	sr := beep.SampleRate(SampleRate)
	err := speaker.Init(sr, sr.N(time.Second/20))
	if err == nil {
		initialized = true
	}
	return err
}

func PlayClick() {
	if !initialized || !enabled {
		return
	}
	sine := &SineWave{Freq: 350, Phase: 0}
	duration := beep.Take(SampleRate/40, sine)
	start := time.Now()
	click := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = duration.Stream(samples)
		for i := range samples {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*40))
			samples[i][0] *= (0.15 * vol)
			samples[i][1] *= (0.15 * vol)
		}
		return n, ok
	})
	speaker.Play(click)
	time.Sleep(50 * time.Millisecond)
}

func PlayAlert() {
	if !initialized || !enabled {
		return
	}
	sine := &SineWave{Freq: 880, Phase: 0}
	beepSound := beep.Take(SampleRate/5, sine)
	start := time.Now()
	alert := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = beepSound.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*3))
			samples[i][0] = samples[i][0] * 0.8 * vol
			samples[i][1] = samples[i][1] * 0.8 * vol
		}
		return n, ok
	})
	speaker.Play(alert)
	time.Sleep(50 * time.Millisecond)
}

func PlayError() {
	if !initialized || !enabled {
		return
	}
	saw := &SawWave{Freq: 110}
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
	time.Sleep(50 * time.Millisecond)
}

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

type SawWave struct {
	Freq  float64
	Phase float64
}

func (s *SawWave) Stream(samples [][2]float64) (int, bool) {
	step := s.Freq / float64(SampleRate)
	for i := range samples {
		v := (s.Phase * 2.0) - 1.0
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
