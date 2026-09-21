// internal/live/live.go
// Purpose: a gpt-live-1 full-duplex session — OpenAI's Live API over WebRTC,
// in CLIENT delegation, where the model hears and speaks and HELIX still does
// all of the reasoning.
//
// WHAT THIS IS NOT. It is not an STT provider and not a TTS provider, and it
// must never be bent into speech.STTProvider or speech.TTSProvider. For the
// life of a session it REPLACES the STT→LLM→TTS chain rather than plugging
// into it: there is no clip to transcribe, no text to synthesise, and no
// provider order to take a place in. internal/speech/chain_order_test.go exists
// because a previous attempt to bend that chain broke provider selection
// silently, and this package is deliberately outside it.
//
// WHY CLIENT DELEGATION, WHICH IS THE WHOLE ARCHITECTURE. The Live API offers
// two modes. In Responses delegation OpenAI's backend model reasons about the
// conversation and decides which tools to call — which would put an external
// model in FRONT of the Instruction Firewall, the risk tiers and the sandbox,
// and §12 guardrail 3 says no input channel bypasses that pipeline. In client
// delegation the model does turn-taking and speech and nothing else: when the
// user finishes a sentence it emits session.delegation.created and waits, and
// Helix's own planner, policy and sandbox serve the turn exactly as they serve
// a typed one. Measured bonus, and it matters: client IS the default, so the
// safe mode is the one you get by configuring nothing (§13).
//
// THE CONSTRAINT THAT SHAPES EVERY CALLER. session.commentary.append is
// PARAPHRASED — and measurement showed it is worse than that. Uninstructed, an
// appended `[ERROR] sandbox violation: /tmp/helix_e2e_evil_1789033489` was
// spoken as "Sandbox violation. That action touched a forbidden path.": the
// path deleted and an explanation INVENTED that Helix never sent. An appended
// name was spoken as "On it." — dropped entirely. So:
//
//	THE SCREEN CARRIES EXACT OUTPUT, printed and unparaphrased, exactly as it
//	does today. GPT-LIVE-1 CARRIES THE CONVERSATION.
//
// DefaultInstructions adds a verbatim rule on top of that, because it measurably
// helps; it is defence in depth and not a licence to route a stack trace
// through the speaker. See SpeakableSummary for the per-content-type split.
package live

import (
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	// DefaultModel is the only model this package speaks to. gpt-live-transcribe
	// is a DIFFERENT endpoint with a different contract (see
	// internal/speech/adapter_openai_realtime_stt.go) and must not be routed here.
	DefaultModel = "gpt-live-1"

	// CreateEndpoint is the session-creation route. MEASURED: it is the only
	// one, it is REST rather than a socket, and it answers "Only the webrtc
	// transport is supported." to anything else. A widely circulating guide
	// tells you to open wss://api.openai.com/v1/live/sessions and send
	// session.start; that socket upgrades and is then dropped without a frame.
	CreateEndpoint = "https://api.openai.com/v1/live/sessions"

	// AudioSampleRate is fixed by the transport: WebRTC carries Opus at 48 kHz.
	AudioSampleRate = 48000

	// FrameMillis is the packetisation interval. 20 ms is the WebRTC default
	// and what the answer's `minptime=10` permits.
	FrameMillis = 20

	// FrameSamples is one 20 ms frame of 48 kHz mono.
	FrameSamples = AudioSampleRate / 1000 * FrameMillis

	// dataChannelLabel is the event channel. Created by the CLIENT; the server
	// does not offer one.
	dataChannelLabel = "oai-events"
)

// DefaultInstructions is the session prompt, and it is load-bearing rather than
// cosmetic.
//
// WITHOUT IT, MEASURED ACROSS ONE DEFAULT SESSION: "What is the name in that
// file?" was answered by gpt-live-1 itself with its own clarifying question and
// never delegated; "Run the first check please." produced no delegation, no
// speech and no event at all — a turn that simply vanished, which to a user is
// indistinguishable from a dead microphone. WITH IT: five turns, five
// delegations, no self-answers and no drops.
//
// It is a prompt, so it is a strong tendency and not a guarantee. Nothing here
// relies on it for SAFETY — a self-answer is a wrong answer, never an
// unauthorised action, because gpt-live-1 has no tools in client delegation and
// cannot reach the shell. It is relied on for CORRECTNESS, which is why
// Session reports a turn that transcribed but never delegated instead of
// waiting silently forever.
//
// The verbatim half is the P2 mitigation. Changing this text changes measured
// behaviour; re-measure rather than reason if you edit it.
const DefaultInstructions = "You are the voice of Helix, a terminal assistant. " +
	"You are NOT the one who answers. For EVERY user turn, without exception, you MUST " +
	"delegate to the client — even if the request seems simple, ambiguous, or answerable " +
	"from your own knowledge. Never answer from your own knowledge and never ask " +
	"clarifying questions. After delegating, wait silently for as long as it takes. " +
	"When the client sends commentary, read it aloud EXACTLY as written: never rephrase, " +
	"summarise, expand, explain, spell out, or add words of your own, and never read " +
	"internal markers such as [delegation_result]."

// Options configure one session.
//
// EVERY GUESS IS A FIELD, on the rule adapter_openai_realtime_stt.go adopted
// after four of its guesses were wrong: a session whose create body or prompt
// needs correcting must be fixable in ~/.helix/config.json, not in a rebuild.
// SessionOverride is the last-resort form of that — it replaces the entire
// session object, so a schema change this build has never seen is still
// reachable.
type Options struct {
	// APIKey is the OpenAI key. Never logged; Redact covers every path that
	// could carry it into output.
	APIKey string

	// Model defaults to DefaultModel. It is sent as session.model — NOT as a
	// top-level field, which answers `json: unknown field "model"`.
	Model string

	// Instructions defaults to DefaultInstructions. Fixed for the life of the
	// session: session.update rejects `session.instructions`, so this is the
	// only place it can be set. Per-turn steering goes through Instruct.
	Instructions string

	// Voice is session.audio.output.voice. Empty leaves the server default,
	// which is "marin".
	Voice string

	// CreateURL overrides CreateEndpoint. The test seam, and the escape hatch
	// if the route moves.
	CreateURL string

	// SessionOverride, when non-empty, is sent as the session object verbatim
	// and every other session field above is ignored.
	SessionOverride []byte

	// HTTPClient performs the create call. Nil uses a client with CreateTimeout.
	HTTPClient *http.Client

	// ICEServers is passed to the peer connection. Usually empty: the service
	// is ice-lite and publishes host candidates on UDP/3478 and TCP/443, so a
	// STUN server buys nothing on an ordinary network.
	ICEServers []webrtc.ICEServer

	// NewCodec builds the Opus codec. Nil uses libopus. Injected by tests so
	// the suite needs neither libopus nor audio hardware (§9 rule 1).
	NewCodec func() (Codec, error)

	// MaxSessionSeconds bounds playback of the model's audio track. Zero uses
	// DefaultMaxSessionSeconds.
	MaxSessionSeconds int
}

// CreateTimeout bounds the session-creation round trip. The measured figure is
// 0.3–0.9 s; 30 s is a stall guard, not a budget.
const CreateTimeout = 30 * time.Second

// DefaultMaxSessionSeconds bounds one duplex session's audio playback.
//
// 30 minutes: three times the AWAKE inactivity stand-down, so the stand-down is
// what ends an abandoned conversation and this is only ever the backstop behind
// it. A session bills per second while it is open, so an unbounded number here
// would be a bill rather than a hang.
const DefaultMaxSessionSeconds = 1800

// Codec is the Opus pair a session needs. An interface so the suite can run
// without libopus installed; the production implementation is opus.go.
type Codec interface {
	Encode(pcm []int16, frameSize int) ([]byte, error)
	Decode(packet []byte) ([]int16, error)
	Close()
}

// opusCodec is the libopus implementation of Codec.
type opusCodec struct {
	enc *Encoder
	dec *Decoder
}

func newOpusCodec() (Codec, error) {
	enc, err := NewEncoder(AudioSampleRate, 1)
	if err != nil {
		return nil, err
	}
	dec, err := NewDecoder(AudioSampleRate, 1)
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &opusCodec{enc: enc, dec: dec}, nil
}

func (c *opusCodec) Encode(pcm []int16, frameSize int) ([]byte, error) {
	return c.enc.Encode(pcm, frameSize)
}

func (c *opusCodec) Decode(packet []byte) ([]int16, error) { return c.dec.Decode(packet) }

func (c *opusCodec) Close() {
	c.enc.Close()
	c.dec.Close()
}
