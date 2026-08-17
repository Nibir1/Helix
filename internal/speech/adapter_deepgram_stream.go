// internal/speech/adapter_deepgram_stream.go
// Purpose: Deepgram WebSocket streaming transcription (BlackBox Phase 7,
// P7.2b). Real-time interim transcripts reduce perceived latency versus the
// batch POST /listen path. The connection sends raw linear16 PCM (16 kHz,
// mono) as binary frames; Deepgram answers JSON Results frames with
// `is_final` distinguishing interim partials from the utterance-final text.
package speech

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// deepgramStreamingSTT adds WebSocket streaming on top of the batch adapter
// (same name/model/key; batch Transcribe/HealthCheck are promoted from the
// embedded value).
type deepgramStreamingSTT struct {
	*deepgramSTT
}

// NewDeepgramStreamingSTT builds a Deepgram adapter that also satisfies
// StreamingSTTProvider. Registered in production so the voice turn can show
// live partials; the batch path remains available via NewDeepgramSTT.
func NewDeepgramStreamingSTT(model, baseURL string) STTProvider {
	if model == "" {
		model = deepgramDefaultModel
	}
	if baseURL == "" {
		baseURL = deepgramDefaultBaseURL
	}
	return &deepgramStreamingSTT{
		deepgramSTT: &deepgramSTT{name: "deepgram", display: "Deepgram", baseURL: baseURL, model: model},
	}
}

// streamURL builds the /v1/listen WebSocket URL. coder/websocket interprets
// the http(s) scheme as ws(s), so the same baseURL works for batch and stream.
func (p *deepgramStreamingSTT) streamURL() string {
	return p.baseURL + "/listen?model=" + p.model +
		"&encoding=linear16&sample_rate=16000&interim_results=true" +
		"&smart_format=true&punctuate=true&endpointing=300&utterance_end_ms=1200"
}

// Stream consumes chunked audio and emits interim/final transcripts until the
// chunk channel closes (which signals end-of-utterance via a CloseStream
// message) or ctx is cancelled.
func (p *deepgramStreamingSTT) Stream(ctx context.Context, chunks <-chan AudioFormat) (<-chan Transcript, error) {
	if p.key == "" {
		return nil, fmt.Errorf("%s: missing API key", p.name)
	}

	conn, _, err := websocket.Dial(ctx, p.streamURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Token " + p.key}},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: dial: %w", p.name, err)
	}

	out := make(chan Transcript, 16)
	done := make(chan struct{})

	// Writer: one concurrent writer is allowed alongside the reader.
	go func() {
		for {
			select {
			case clip, ok := <-chunks:
				if !ok {
					// Tell Deepgram the utterance ended so it flushes finals.
					// Do NOT close the conn here — the server closes after
					// flushing, and the reader drains until then.
					_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"CloseStream"}`))
					return
				}
				pcm, err := toLinear16(clip)
				if err != nil {
					continue // skip an undecodable chunk; streaming is best-effort
				}
				if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
					return
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()

	// Reader: emits Results frames until the server closes or ctx cancels.
	// Deepgram's `is_final` only finalizes a SEGMENT of the utterance; the
	// utterance itself ends at `speech_final` (or an UtteranceEnd frame).
	// Finalizing on the first is_final used to truncate commands at natural
	// mid-sentence pauses — so segments accumulate here and the caller only
	// sees IsFinal=true once endpointing actually fired.
	go func() {
		defer close(out)
		defer close(done)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		var segments []string
		joined := func(extra string) string {
			parts := segments
			if extra != "" {
				parts = append(append([]string{}, segments...), extra)
			}
			return strings.TrimSpace(strings.Join(parts, " "))
		}
		emit := func(t Transcript) bool {
			select {
			case out <- t:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				// Connection ended with accumulated finals: flush them so a
				// server-side close never eats a transcribed utterance.
				if text := joined(""); text != "" {
					emit(Transcript{Text: text, Provider: "deepgram", IsFinal: true})
				}
				return
			}
			if typ != websocket.MessageText {
				continue
			}

			t, segFinal, kind := parseDeepgramFrame(data)
			switch kind {
			case dgFrameUtteranceEnd:
				if text := joined(""); text != "" {
					if !emit(Transcript{Text: text, Provider: "deepgram", IsFinal: true}) {
						return
					}
					segments = nil
				}
				continue
			case dgFrameOther:
				continue
			}

			text := strings.TrimSpace(t.Text)
			switch {
			case t.IsFinal: // speech_final: the utterance is complete
				if text != "" {
					segments = append(segments, text)
				}
				full := joined("")
				segments = nil
				if full == "" {
					continue
				}
				t.Text = full
				if !emit(t) {
					return
				}
			case segFinal: // segment locked in, utterance continues
				if text != "" {
					segments = append(segments, text)
				}
				if full := joined(""); full != "" {
					t.Text = full
					t.IsFinal = false
					if !emit(t) {
						return
					}
				}
			default: // interim partial
				if full := joined(text); full != "" {
					t.Text = full
					if !emit(t) {
						return
					}
				}
			}
		}
	}()

	return out, nil
}

// toLinear16 converts a captured clip to raw 16-bit little-endian mono PCM,
// the only encoding Deepgram streaming accepts here.
func toLinear16(clip AudioFormat) ([]byte, error) {
	switch clip.Kind {
	case KindPCM:
		return clip.Bytes, nil
	case KindWAV:
		samples, err := DecodeWAVPCM16(clip.Bytes)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 2*len(samples))
		for i, s := range samples {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported audio kind %q for streaming", clip.Kind)
	}
}

// dgFrameKind classifies a Deepgram streaming frame for the reader loop.
type dgFrameKind int

const (
	dgFrameResults dgFrameKind = iota
	dgFrameUtteranceEnd
	dgFrameOther
)

// parseDeepgramFrame decodes one Deepgram streaming frame. For Results frames
// the returned Transcript carries IsFinal = speech_final (utterance complete);
// segmentFinal reports is_final (this SEGMENT is locked in, utterance may
// continue). Metadata/SpeechStarted/Error frames classify as dgFrameOther.
func parseDeepgramFrame(data []byte) (t Transcript, segmentFinal bool, kind dgFrameKind) {
	var msg struct {
		Type        string `json:"type"`
		IsFinal     bool   `json:"is_final"`
		SpeechFinal bool   `json:"speech_final"`
		Channel     struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"channel"`
		Metadata struct {
			Language string `json:"language"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return Transcript{}, false, dgFrameOther
	}
	if msg.Type == "UtteranceEnd" {
		return Transcript{}, false, dgFrameUtteranceEnd
	}
	if msg.Type != "Results" || len(msg.Channel.Alternatives) == 0 {
		return Transcript{}, false, dgFrameOther
	}
	alt := msg.Channel.Alternatives[0]
	return Transcript{
		Text:       alt.Transcript,
		Language:   msg.Metadata.Language,
		Confidence: alt.Confidence,
		Provider:   "deepgram",
		IsFinal:    msg.SpeechFinal,
	}, msg.IsFinal, dgFrameResults
}

// parseDeepgramTranscript is the legacy single-frame view kept for tests:
// Results frames map to a Transcript whose IsFinal reflects segment finality.
func parseDeepgramTranscript(data []byte) (Transcript, bool) {
	t, segFinal, kind := parseDeepgramFrame(data)
	if kind != dgFrameResults {
		return Transcript{}, false
	}
	if !segFinal && !t.IsFinal && strings.TrimSpace(t.Text) == "" {
		return Transcript{}, false
	}
	t.IsFinal = t.IsFinal || segFinal
	return t, true
}
