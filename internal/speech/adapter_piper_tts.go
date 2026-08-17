// internal/speech/adapter_piper_tts.go
// Purpose: Piper TTS sidecar adapter (rhasspy piper-http style service at
// /api/tts, returning WAV). Local, free, offline — the private TTS default
// (ADR-002).
package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const piperDefaultBaseURL = "http://127.0.0.1:5000"

// piperTTS implements TTSProvider against a local piper-http sidecar.
type piperTTS struct {
	name    string
	display string
	baseURL string
}

// NewPiperTTS builds the Piper sidecar adapter.
func NewPiperTTS(baseURL string) TTSProvider {
	if baseURL == "" {
		baseURL = piperDefaultBaseURL
	}
	return &piperTTS{name: "piper-local", display: "Piper (local sidecar)", baseURL: baseURL}
}

func (p *piperTTS) Name() string         { return p.name }
func (p *piperTTS) DisplayName() string  { return p.display }
func (p *piperTTS) SetAPIKey(string)     {} // local service: no key
func (p *piperTTS) RequiresAPIKey() bool { return false }
func (p *piperTTS) IsLocal() bool        { return true }
func (p *piperTTS) DefaultModel() string { return "piper" }

// Synthesize fetches WAV bytes for the text from the sidecar.
func (p *piperTTS) Synthesize(ctx context.Context, text string, _ SynthesisOptions) (AudioFormat, error) {
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	u := p.baseURL + "/api/tts?" + url.Values{"text": {text}}.Encode()
	data, err := sharedClient.DoRaw(ctx, http.MethodGet, u, nil, "", nil)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" {
		return AudioFormat{}, fmt.Errorf("%s: unexpected non-WAV response (%d bytes)", p.name, len(data))
	}

	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// piperMaxHeaderBytes bounds how much of a response may be consumed looking for
// the `data` chunk. A real WAV header is 44 bytes; anything past this is not a
// header Helix can stream, and a hostile/broken sidecar must not be able to make
// this read forever.
const piperMaxHeaderBytes = 64 << 10

// SynthesizeStream streams the sidecar's WAV body as PCM (P7.2c).
//
// Piper's HTTP service has no headerless response format, so instead of asking
// for one this consumes the RIFF header off the front of the response and hands
// the caller the reader positioned at the first PCM sample. That is safe with a
// container the cloud adapters avoid, because the header is small, fixed, and
// arrives before any audio — the reason the OpenAI adapter asks for raw `pcm`
// is that it cannot know its rate up front, whereas here the bytes state it.
//
// This matters most on the edge box: piper is the offline TTS default, so
// without streaming the one deployment with no cloud fallback was also the one
// that always waited for a whole synthesis before speaking.
//
// Args:
//   - ctx: cancels the request and, through the body, playback.
//   - text: the utterance.
//   - _: piper's HTTP API exposes no per-request voice/speed knobs.
//
// Returns: the open body positioned at the PCM, or an error before any audio
// played — the caller's cue to fall back to the buffered path.
// Complexity: O(1) request plus the header read; the body streams.
func (p *piperTTS) SynthesizeStream(
	ctx context.Context, text string, _ SynthesisOptions,
) (StreamedAudio, error) {
	if text == "" {
		return StreamedAudio{}, fmt.Errorf("%s: empty text", p.name)
	}

	u := p.baseURL + "/api/tts?" + url.Values{"text": {text}}.Encode()
	resp, err := sharedClient.DoRequest(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return StreamedAudio{}, fmt.Errorf("%s: %w", p.name, err)
	}

	rate, channels, err := readWAVStreamHeader(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return StreamedAudio{}, fmt.Errorf("%s: %w", p.name, err)
	}

	return StreamedAudio{SampleRate: rate, Channels: channels, Body: resp.Body}, nil
}

// readWAVStreamHeader consumes a RIFF/WAVE header from r, leaving the reader
// positioned at the first byte of PCM sample data.
//
// It insists on 16-bit integer PCM: StreamedAudio's consumer decodes fixed
// 16-bit little-endian frames, so a float or 8-bit body would play as noise.
// Rejecting it here means the caller falls back to the buffered path (which
// decodes the container properly) instead of producing garbage.
//
// Args:
//   - r: the response body, positioned at the start of the RIFF header.
//
// Returns: sample rate, channel count, or an error naming what was wrong.
// Complexity: O(header size), bounded by piperMaxHeaderBytes.
func readWAVStreamHeader(r io.Reader) (sampleRate int, channels int, err error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return 0, 0, fmt.Errorf("wav stream: short RIFF header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return 0, 0, errors.New("wav stream: not a RIFF/WAVE response")
	}

	consumed := len(riff)
	var haveFmt bool
	for consumed < piperMaxHeaderBytes {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return 0, 0, fmt.Errorf("wav stream: truncated chunk header: %w", err)
		}
		consumed += len(head)
		id := string(head[0:4])
		size := int(binary.LittleEndian.Uint32(head[4:8]))

		if id == "data" {
			if !haveFmt {
				return 0, 0, errors.New("wav stream: data chunk before fmt chunk")
			}
			return sampleRate, channels, nil
		}

		// Chunks are word-aligned, so an odd length carries one pad byte.
		body := size
		if size%2 == 1 {
			body++
		}
		if body < 0 || consumed+body > piperMaxHeaderBytes {
			return 0, 0, errors.New("wav stream: header exceeds the size bound")
		}
		buf := make([]byte, body)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, 0, fmt.Errorf("wav stream: truncated %q chunk: %w", id, err)
		}
		consumed += body

		if id == "fmt " {
			if size < 16 {
				return 0, 0, errors.New("wav stream: malformed fmt chunk")
			}
			if format := binary.LittleEndian.Uint16(buf[0:2]); format != 1 {
				return 0, 0, fmt.Errorf(
					"wav stream: format tag %d is not 16-bit integer PCM", format)
			}
			if bits := binary.LittleEndian.Uint16(buf[14:16]); bits != 16 {
				return 0, 0, fmt.Errorf("wav stream: %d bits per sample, want 16", bits)
			}
			channels = int(binary.LittleEndian.Uint16(buf[2:4]))
			sampleRate = int(binary.LittleEndian.Uint32(buf[4:8]))
			if sampleRate <= 0 || channels <= 0 {
				return 0, 0, errors.New("wav stream: fmt chunk declares no audio")
			}
			haveFmt = true
		}
	}
	return 0, 0, errors.New("wav stream: no data chunk within the header bound")
}

// HealthCheck treats any HTTP response as proof the sidecar is alive.
func (p *piperTTS) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := sharedClient.RawClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
