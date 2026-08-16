// internal/wakeword/sidecar.go
// Purpose: HTTP client for an openWakeWord-class sidecar service (ADR-002).
// Contract (documented for whoever runs the sidecar):
//
//	POST {base}/predict
//	Content-Type: audio/wav  (raw body = WAV bytes)
//	200 {"scores": {"hey_helix": 0.82, ...}}   // openWakeWord-shaped
//	200 {"score": 0.82}                        // simple single-phrase shape
//
// The phrase key is the configured wake phrase with spaces mapped to
// underscores. Health: GET {base}/health → any response = alive.
package wakeword

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"helix/internal/speech"
)

// SidecarDetector scores chunks against a local wake-word model service.
type SidecarDetector struct {
	BaseURL   string
	Phrase    string
	Threshold float64
	Client    *http.Client
}

// NewSidecarDetector builds a detector with preset-mapped thresholds.
func NewSidecarDetector(baseURL, phrase string, preset Preset) *SidecarDetector {
	threshold := 0.55
	switch preset {
	case PresetStrict:
		threshold = 0.70
	case PresetLoose:
		threshold = 0.40
	}
	if phrase == "" {
		phrase = "hey helix"
	}
	return &SidecarDetector{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Phrase:    phrase,
		Threshold: threshold,
		Client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Wake posts one chunk and evaluates the phrase score.
func (d *SidecarDetector) Wake(clip speech.AudioFormat) (score float64, woke bool, err error) {
	if clip.Kind != speech.KindWAV {
		return 0, false, fmt.Errorf("sidecar: only WAV chunks supported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL+"/predict", bytes.NewReader(clip.Bytes))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "audio/wav")

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("sidecar unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return 0, false, fmt.Errorf("sidecar HTTP %d", resp.StatusCode)
	}

	var out struct {
		Scores map[string]float64 `json:"scores"`
		Score  float64            `json:"score"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return 0, false, fmt.Errorf("sidecar response: %w", err)
	}

	key := strings.ReplaceAll(strings.ToLower(d.Phrase), " ", "_")
	if s, ok := out.Scores[key]; ok {
		score = s
	} else if len(out.Scores) > 0 {
		// Take the max score if the key naming differs.
		for _, v := range out.Scores {
			if v > score {
				score = v
			}
		}
	} else {
		score = out.Score
	}
	return score, score >= d.Threshold, nil
}

// Health probes the sidecar (any HTTP response = alive).
func (d *SidecarDetector) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("sidecar unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
