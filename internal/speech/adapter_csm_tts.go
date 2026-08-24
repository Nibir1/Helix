// internal/speech/adapter_csm_tts.go
// Purpose: Sesame CSM-1B as a local TTS provider — the most natural-sounding
// voice Helix can produce with nothing leaving the machine.
//
// WHAT CSM ACTUALLY IS, because the marketing and the release differ. Sesame's
// "Crossing the uncanny valley of voice" describes a full conversational system;
// what they open-sourced (Apache-2.0) is the speech GENERATOR from it. The model
// card is blunt: "CSM is trained to be an audio generation model and not a
// general-purpose multimodal LLM. It cannot generate text." So it is a TTSProvider
// here and nothing more — Helix's planner remains the thing that decides what to
// say, and whisper.cpp remains the thing that hears.
//
// Architecture, which explains both the quality and the cost: a Llama-1B backbone
// consumes interleaved text and audio and predicts the zeroth Mimi codebook, and a
// small (~100M) decoder predicts the remaining acoustic codebooks. Mimi is a
// split-RVQ neural codec running at 12.5 Hz, so one second of speech is twelve and
// a half autoregressive frames through a 1B transformer — that is why this wants a
// GPU where Piper wants almost nothing.
//
// NO PYTHON. The reference implementation is PyTorch, which this deliberately does
// not use. The sidecar is `csm.rs` (cartesia-one/csm.rs), a Rust/candle build with
// CUDA, Metal, Accelerate and MKL backends and an OpenAI-shaped HTTP server. That
// keeps ADR-002 intact: an external local HTTP service, no container runtime, and
// nothing linked into Helix's CGO-free binary.
package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"helix/internal/providers"
)

// csmLocalDefaultURL is where Helix expects the CSM sidecar.
//
// Deliberately NOT csm.rs's own documented default of 8080. That port is already
// whisper.cpp's default and llama.cpp's default, so a user running a local STT
// chain or a local brain — the exact user who wants CSM — would collide on the
// first launch. local_runtimes.md §2 exists because this has already happened
// twice. Helix reassigns ports per provider anyway; this is the value it starts
// from, and the docs tell the user to pass a matching --port.
const csmLocalDefaultURL = "http://127.0.0.1:28195"

// CSMDefaultEndpoint is the same value, exported so cmd/helix does not need a
// mirrored copy.
//
// The other sidecars keep private defaults and cmd/helix duplicates them behind
// a comment promising "a drift test pins them to the adapters" — there is no such
// test, and two constants that must agree with nothing enforcing it is how the
// endpoint bugs in this package started. One exported value cannot drift.
const CSMDefaultEndpoint = csmLocalDefaultURL

// csmDefaultSpeaker is CSM's speaker 0.
//
// Speaker identity in CSM is a conditioning token, not a voice file: the model was
// trained on multi-speaker conversations where the speaker is encoded in the text
// stream. Speaker 0 is the conventional "assistant" slot in Sesame's own examples.
const csmDefaultSpeaker = 0

// csmDefaultTemperature keeps prosody varied without letting the acoustic decoder
// wander. CSM is autoregressive over audio codes, so temperature affects HOW a
// sentence is said rather than what is said — too low is flat, too high slurs.
const csmDefaultTemperature = 0.7

type csmTTS struct {
	name    string
	display string
	origin  string
	model   string

	// speaker and temperature are CSM's two knobs. They ride the OpenAI request
	// as extra fields, which csm.rs reads and a stricter OpenAI server would
	// ignore — so this adapter stays safe to point at either.
	speaker     int
	temperature float64

	// contextRejected records that this endpoint answered a context-carrying
	// request with a 4xx, so later calls stop sending one.
	//
	// This is the whole safety story for an extension no upstream server
	// implements yet. Sending an unknown field to a strict deserializer is a
	// 400, and a 400 here means Helix goes silent — so the first rejection
	// degrades this provider to plain single-utterance synthesis for the rest
	// of the session and the turn still gets spoken. Context can only ever make
	// the voice better, never make it absent.
	contextRejected bool

	// contextHonored / contextIgnored record what the server said about the
	// context it was sent: honored when it reported a segment count, ignored
	// when it returned no such header at all. An unpatched server lands in the
	// second case, and saying so is the difference between reporting a feature
	// and reporting a wish.
	contextHonored bool
	contextIgnored bool

	mu sync.Mutex
}

// NewCSMLocalTTS builds the CSM sidecar adapter.
//
// voice carries the speaker id when it parses as a number, because the wizard's
// existing "voice" prompt is the natural place to ask and CSM has no voice names.
func NewCSMLocalTTS(model, voice, baseURL string) TTSProvider {
	if baseURL == "" {
		baseURL = csmLocalDefaultURL
	}
	if model == "" {
		model = "sesame/csm-1b"
	}
	return &csmTTS{
		name:        "csm-local",
		display:     "Sesame CSM-1B (local sidecar)",
		origin:      serverOrigin(baseURL),
		model:       model,
		speaker:     csmSpeakerFromVoice(voice),
		temperature: csmDefaultTemperature,
	}
}

// csmSpeakerFromVoice reads a speaker id out of the configured voice field.
//
// Anything unparseable falls back to speaker 0 rather than erroring: a wrong
// speaker is a different-sounding voice, not a failure, and refusing to speak
// because someone typed "alloy" out of habit would be a poor trade.
func csmSpeakerFromVoice(voice string) int {
	v := strings.TrimSpace(voice)
	if v == "" {
		return csmDefaultSpeaker
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
		return n
	}
	return csmDefaultSpeaker
}

func (p *csmTTS) Name() string        { return p.name }
func (p *csmTTS) DisplayName() string { return p.display }

// RequiresAPIKey is false: a local sidecar has no account, no key and no
// per-call cost. That is the whole point of running a 1B model on your own GPU.
func (p *csmTTS) RequiresAPIKey() bool { return false }
func (p *csmTTS) IsLocal() bool        { return true }

// SetAPIKey is a no-op — there is nothing to authenticate to.
func (p *csmTTS) SetAPIKey(string) {}

// DefaultModel names the weights the sidecar loads.
func (p *csmTTS) DefaultModel() string { return p.model }

// Synthesize renders text to a WAV clip through the CSM sidecar.
func (p *csmTTS) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	speaker := p.speaker
	if opts.Voice != "" {
		speaker = csmSpeakerFromVoice(opts.Voice)
	}

	// Context is attempted first and dropped permanently on rejection, so a
	// server that does not understand it costs one failed request per session
	// rather than every reply.
	withContext := p.contextAllowed() && len(opts.Context) > 0

	data, applied, err := p.post(ctx, text, speaker, opts.Context, withContext)
	if err != nil && withContext && isContextRejection(err) {
		p.disableContext()
		data, applied, err = p.post(ctx, text, speaker, nil, false)
	}
	if withContext {
		p.recordContextOutcome(applied)
	}
	if err != nil {
		return AudioFormat{}, p.diagnose(err)
	}
	if len(data) == 0 {
		return AudioFormat{}, fmt.Errorf("%s: server returned no audio", p.name)
	}

	rate, channels, werr := wavHeaderInfo(data)
	if werr != nil {
		// CSM synthesizes at 24kHz; if the body is not WAV something upstream
		// changed and guessing a rate would play it at the wrong pitch.
		return AudioFormat{}, fmt.Errorf("%s: response is not WAV audio: %w", p.name, werr)
	}

	return AudioFormat{
		Kind:       KindWAV,
		SampleRate: rate,
		Channels:   channels,
		Bytes:      data,
	}, nil
}

// HealthCheck probes the sidecar.
func (p *csmTTS) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.origin+"/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := sharedClient.RawClient().Do(req)
	if err != nil {
		return p.diagnose(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Any answer proves a server is there. csm.rs may not implement /v1/models,
	// and a 404 from the right process is still better evidence than a refused
	// connection — the same judgement the whisper and piper probes make.
	if resp.StatusCode >= 500 {
		return p.diagnose(fmt.Errorf("%s returned HTTP %d", p.name, resp.StatusCode))
	}
	return nil
}

// diagnose turns a transport error into guidance naming the configured port.
func (p *csmTTS) diagnose(err error) error {
	return LocalDiagnosis(p.name, p.origin, csmStartCmd(p.origin), csmCfgKey, err)
}

// post builds and sends one synthesis request.
func (p *csmTTS) post(ctx context.Context, text string, speaker int,
	turns []ConversationTurn, withContext bool) ([]byte, int, error) {

	body := map[string]any{
		"model": p.model,
		"input": text,
		// `voice` is sent for OpenAI-schema compatibility and ignored by CSM,
		// which conditions on speaker_id instead.
		"voice":           fmt.Sprint(speaker),
		"response_format": "wav",
		"speaker_id":      speaker,
		"temperature":     p.temperature,
	}
	if withContext {
		if segs := encodeContext(turns); len(segs) > 0 {
			body["context"] = segs
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, -1, err
	}
	// No Authorization header on purpose: this is a loopback sidecar the user
	// started. Sending an empty bearer token makes some servers 401 rather than
	// ignore it.
	data, hdr, err := sharedClient.DoRawWithHeaders(ctx, http.MethodPost,
		p.origin+"/v1/audio/speech", nil, "application/json", payload)
	return data, contextSegmentsApplied(hdr), err
}

// contextSegmentsApplied reads how many context segments the server actually
// conditioned on.
//
// Returns -1 when the server said nothing, which is the important case rather
// than an edge one: serde ignores unknown fields by default, so an unpatched
// csm.rs ACCEPTS a context field and silently drops it. Without this header
// "the request succeeded" would be indistinguishable from "the context was
// used", and Helix would claim conversational prosody it is not getting.
func contextSegmentsApplied(hdr http.Header) int {
	if hdr == nil {
		return -1
	}
	v := strings.TrimSpace(hdr.Get("X-CSM-Context-Segments"))
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

// recordContextOutcome remembers whether the sidecar honored context.
func (p *csmTTS) recordContextOutcome(applied int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case applied > 0:
		p.contextHonored = true
		p.contextIgnored = false
	case applied == 0:
		// The server understands the field and used nothing — that is a real
		// answer, not silence.
		p.contextHonored = true
	default:
		p.contextIgnored = true
	}
}

// ContextStatus reports what is actually happening with conversational context,
// for status output that must not overstate.
func (p *csmTTS) ContextStatus() (honored, ignored, rejected bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.contextHonored, p.contextIgnored, p.contextRejected
}

// contextSegment is one prior turn on the wire.
//
// THIS IS A HELIX EXTENSION, not an upstream contract. No CSM server implements
// it yet: csm.rs's HTTP API is stateless single-utterance, which is precisely the
// gap that keeps a local CSM sounding like very good TTS rather than a
// participant in the conversation. The shape mirrors the reference Python API's
// Segment(text, speaker, audio) so a server adding support has an obvious target,
// and docs/local_runtimes.md §3.6 specifies it for exactly that reason.
type contextSegment struct {
	Speaker int    `json:"speaker"`
	Text    string `json:"text"`
	Audio   string `json:"audio_b64,omitempty"`
	Format  string `json:"format,omitempty"`
}

// encodeContext renders retained turns for the wire, oldest first.
//
// Turns with no audio still go: CSM conditions on text as well, and a reply that
// was printed but never spoken (TTS off) is still part of what was said.
func encodeContext(turns []ConversationTurn) []contextSegment {
	out := make([]contextSegment, 0, len(turns))
	for _, t := range turns {
		if strings.TrimSpace(t.Text) == "" && len(t.Audio.Bytes) == 0 {
			continue
		}
		seg := contextSegment{Speaker: t.Speaker, Text: t.Text}
		if len(t.Audio.Bytes) > 0 {
			seg.Audio = base64.StdEncoding.EncodeToString(t.Audio.Bytes)
			seg.Format = string(t.Audio.Kind)
		}
		out = append(out, seg)
	}
	return out
}

// contextAllowed reports whether context may still be sent to this endpoint.
func (p *csmTTS) contextAllowed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.contextRejected
}

// disableContext remembers that this endpoint refused a context request.
func (p *csmTTS) disableContext() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.contextRejected = true
}

// ContextRejected reports whether context was tried and refused, so status
// output can say "this sidecar does not support it" rather than implying Helix
// simply chose not to.
func (p *csmTTS) ContextRejected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.contextRejected
}

// isContextRejection reports whether an error looks like the server refusing the
// REQUEST rather than failing to serve it.
//
// A 4xx means the payload was unacceptable, which for a request that differs
// from a known-good one only by the context field is the field being rejected. A
// 5xx or a transport error is a broken or absent server and says nothing about
// context, so retrying without it would just fail twice.
func isContextRejection(err error) bool {
	if err == nil {
		return false
	}
	var se *providers.StatusError
	if errors.As(err, &se) {
		return se.Code >= 400 && se.Code < 500
	}
	return false
}
