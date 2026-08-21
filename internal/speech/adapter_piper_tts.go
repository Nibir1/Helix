// internal/speech/adapter_piper_tts.go
// Purpose: Piper TTS sidecar adapter. Local, free, offline — the private TTS
// default (ADR-002).
//
// Route note: piper's own HTTP server (`python3 -m piper.http_server`) serves
// synthesis at the ROOT path — GET /?text=... or POST / with the text as the
// body — and returns audio/wav. This adapter previously requested /api/tts,
// which is the older Rhasspy TTS API and does not exist on piper's server, so
// against a stock `piper.http_server` every synthesis returned HTTP 404: the
// offline TTS default was unusable as shipped. Both routes are now tried, root
// first, and the winner is cached.
package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// piperDefaultBaseURL is piper.http_server's default bind address.
//
// On macOS this port is a known hazard: AirPlay Receiver binds 5000 by default,
// answers HTTP, and is not piper. That is why the health check below insists on
// actual WAV bytes rather than accepting any response — see its comment.
const piperDefaultBaseURL = "http://127.0.0.1:5000"

const (
	// piperRootRoute is piper.http_server's synthesis path.
	piperRootRoute = "/"

	// piperRhasspyRoute is the older Rhasspy TTS HTTP API path, kept as a
	// fallback for people running that service.
	piperRhasspyRoute = "/api/tts"
)

// piperTTS implements TTSProvider against a local piper HTTP sidecar.
type piperTTS struct {
	name    string
	display string
	origin  string
	routes  []string

	routeMu sync.Mutex
	route   string
}

// NewPiperTTS builds the Piper sidecar adapter.
func NewPiperTTS(baseURL string) TTSProvider {
	if baseURL == "" {
		baseURL = piperDefaultBaseURL
	}
	return &piperTTS{
		name:    "piper-local",
		display: "Piper (local sidecar)",
		origin:  serverOrigin(baseURL),
		routes:  dedupeRoutes(piperRootRoute, piperRhasspyRoute),
	}
}

// synthURL builds the request URL for one route.
func (p *piperTTS) synthURL(route, text string) string {
	q := "?" + url.Values{"text": {text}}.Encode()
	if route == "/" {
		return p.origin + "/" + q
	}
	return p.origin + route + q
}

// candidateRoutes returns the cached winner first, then the rest.
func (p *piperTTS) candidateRoutes() []string {
	p.routeMu.Lock()
	cached := p.route
	p.routeMu.Unlock()

	if cached == "" {
		return p.routes
	}
	out := make([]string, 0, len(p.routes))
	out = append(out, cached)
	for _, r := range p.routes {
		if r != cached {
			out = append(out, r)
		}
	}
	return out
}

func (p *piperTTS) rememberRoute(route string) {
	p.routeMu.Lock()
	p.route = route
	p.routeMu.Unlock()
}

// ActiveRoute reports the synthesis path that last answered ("" before the
// first success), for /voice-status.
func (p *piperTTS) ActiveRoute() string {
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	return p.route
}

// Endpoint reports the server origin, for diagnostics and conflict detection.
func (p *piperTTS) Endpoint() string { return p.origin }

func (p *piperTTS) Name() string         { return p.name }
func (p *piperTTS) DisplayName() string  { return p.display }
func (p *piperTTS) SetAPIKey(string)     {} // local service: no key
func (p *piperTTS) RequiresAPIKey() bool { return false }
func (p *piperTTS) IsLocal() bool        { return true }
func (p *piperTTS) DefaultModel() string { return "piper" }

// Synthesize fetches WAV bytes for the text from the sidecar, trying each known
// route until one returns actual audio.
//
// A non-WAV 200 is treated exactly like a 404: it means something that is not
// piper is answering on this port (on macOS, most often AirPlay Receiver on
// 5000). Continuing to the next route, and ultimately failing with that stated,
// is far more useful than handing the player a chunk of HTML.
func (p *piperTTS) Synthesize(ctx context.Context, text string, _ SynthesisOptions) (AudioFormat, error) {
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	var lastErr error
	for _, route := range p.candidateRoutes() {
		data, err := sharedClient.DoRaw(ctx, http.MethodGet, p.synthURL(route, text), nil, "", nil)
		if err != nil {
			lastErr = err
			// Any 4xx means "not here" on THIS route: 404 for a missing path,
			// 403/401 for a server that refuses everything (an AirPlay Receiver
			// squatting on port 5000 does exactly this). Walk on and let the
			// diagnosis below explain whatever the last one was. Only a 404 used
			// to continue, so a 403 aborted the walk and surfaced as a bare
			// "HTTP 403:" with an empty body and no explanation.
			if isClientError(err) && len(p.routes) > 1 {
				continue
			}
			return AudioFormat{}, p.diagnose(err)
		}
		if len(data) < 44 || string(data[:4]) != "RIFF" {
			lastErr = fmt.Errorf("answered on %s with %d bytes that are not WAV", route, len(data))
			continue
		}

		rate, channels, werr := wavHeaderInfo(data)
		if werr != nil {
			lastErr = werr
			continue
		}
		p.rememberRoute(route)
		return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no synthesis route responded")
	}
	return AudioFormat{}, p.diagnose(lastErr)
}

// diagnose turns a failure into an explanation naming the endpoint, the likely
// cause, and the fix.
func (p *piperTTS) diagnose(err error) error {
	return LocalDiagnosis(p.name, p.origin, piperStartCmd, piperCfgKey, err)
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

	var lastErr error
	for _, route := range p.candidateRoutes() {
		resp, err := sharedClient.DoRequest(ctx, http.MethodGet, p.synthURL(route, text), nil, nil)
		if err != nil {
			lastErr = err
			continue
		}
		rate, channels, herr := readWAVStreamHeader(resp.Body)
		if herr != nil {
			_ = resp.Body.Close()
			lastErr = herr
			continue
		}
		p.rememberRoute(route)
		return StreamedAudio{SampleRate: rate, Channels: channels, Body: resp.Body}, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no synthesis route responded")
	}
	return StreamedAudio{}, p.diagnose(lastErr)
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

// HealthCheck synthesizes one short word and insists on getting WAV back.
//
// It used to accept ANY HTTP response as proof of life, which on macOS is
// actively wrong: AirPlay Receiver owns port 5000 by default and answers HTTP,
// so /voice-status reported piper "reachable" on a machine where piper was not
// running at all and every spoken reply failed. Requiring real audio is the only
// probe that tells those two situations apart, and it doubles as route
// discovery.
func (p *piperTTS) HealthCheck(ctx context.Context) error {
	if _, err := p.Synthesize(ctx, "ok", SynthesisOptions{}); err != nil {
		return err
	}
	return nil
}
