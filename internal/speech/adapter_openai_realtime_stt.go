// internal/speech/adapter_openai_realtime_stt.go
// Purpose: OpenAI realtime transcription over WebSocket — live partials from
// the same vendor and the same key as the rest of the OpenAI chain.
//
// WHAT IS VERIFIED, from OpenAI's model documentation:
//
//	model     gpt-live-transcribe
//	endpoint  v1/realtime/transcription_sessions
//	modality  audio + text in, text out
//	price     $0.017 per minute of realtime audio
//
// ALSO VERIFIED, from the realtime transcription guide, which publishes the
// session shape naming gpt-live-transcribe directly:
//
//	session frame  {"type":"session.update","session":{"type":"transcription",
//	                "audio":{"input":{"format":{"type":"audio/pcm","rate":N},
//	                "transcription":{"model":"gpt-live-transcribe"},
//	                "turn_detection":null}}}}
//	audio in       input_audio_buffer.append, base64-encoded PCM
//	end of turn    input_audio_buffer.commit
//	beta header    "Remove the OpenAI-Beta: realtime=v1 header when calling the
//	                GA interface" — so it is NOT sent
//
// The first version of this file guessed at that session frame and was wrong
// in four places, every one a flat key where the real API nests under
// session.audio.input. The framing and the commit were guessed right. The
// corrections are recorded on sessionFrame itself.
//
// WHAT IS STILL INFERRED: the WebSocket URL. The transport pages are not
// reachable, so the dial target is assembled from the documented endpoint path
// and may be wrong; `route` and HELIX_OPENAI_REALTIME_URL exist to correct it
// without a rebuild.
//
// A wrong guess fails SILENTLY — the socket opens, the HUD meters a live
// microphone, and nothing is ever transcribed, which reads to the user as a
// broken microphone rather than a broken adapter. Three things make that
// survivable, and none of them is optional:
//
//  1. Every guess is configuration (speech.RealtimeConfig), including a
//     `session` field sent byte for byte, so the real specification can be
//     pasted into ~/.helix/config.json without a rebuild.
//  2. The frame parser is TOLERANT: it classifies by the shape of the `type`
//     string rather than matching exact event names, and ignores what it does
//     not recognise.
//  3. A session that closes having transcribed nothing AND having reported an
//     error returns a dial-class failure, so the voice turn falls back to
//     batch instead of blaming the microphone.
//
// OUT OF SCOPE, and checked again rather than assumed: gpt-live-1's only
// endpoint is v1/live/sessions, its quickstart and transport pages are not
// published in a form that can be read, and the Free tier cannot reach it at
// all. It is also full duplex — the
// model speaks as well as listens, which replaces the entire STT→LLM→TTS chain
// rather than plugging into it, and needs simultaneous capture and playback
// that this codebase cannot do without acoustic echo cancellation. It is
// reachable by typing its ID once the model picker stopped hardcoding models;
// it is not implemented here.
package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const (
	// openaiRealtimeSTTModel is the documented transcription model.
	openaiRealtimeSTTModel = "gpt-live-transcribe"

	// openaiRealtimeRoute is the documented endpoint. INFERRED: that it is
	// reached by dialling it as a WebSocket directly.
	openaiRealtimeRoute = "/v1/realtime/transcription_sessions"
)

// realtimeSTTModels are the models routed to the WebSocket path.
//
// An allowlist rather than a broad substring match, so `whisper-1` never
// attempts a socket dial and falls back — one wasted round trip per turn, the
// same defect the ollamaToolModels gate exists to prevent. It also keeps
// gpt-live-1 OUT: that is a different endpoint with a different contract, and
// silently routing it here would produce a confusing failure rather than an
// honest "not supported".
var realtimeSTTModels = map[string]bool{
	openaiRealtimeSTTModel: true,
	"gpt-realtime-2.1":     true,
}

// IsRealtimeSTTModel reports whether a model should use the WebSocket path.
func IsRealtimeSTTModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if realtimeSTTModels[m] {
		return true
	}
	// A forward-looking allowance for the same family, deliberately narrow.
	return strings.Contains(m, "live-transcribe") || strings.HasPrefix(m, "gpt-realtime")
}

// openaiRealtimeSTT adds WebSocket streaming on top of the batch OpenAI
// adapter.
//
// Embedded, not standalone, and the provider NAME stays "openai". Keys are
// hydrated per provider name (stt.<name>), and adoptAIKeyForSpeech /
// adoptSiblingSpeechKey copy them by exact vendor name — so a new name like
// "openai-realtime" would ask the user for the same OpenAI key a third time,
// which is precisely what those helpers exist to prevent.
type openaiRealtimeSTT struct {
	*openaiSTT
	rc *RealtimeConfig

	// faultMu guards fault, which the reader goroutine writes and the caller
	// reads after the stream closes.
	faultMu sync.Mutex
	fault   error
}

// StreamFault reports why a session produced nothing, satisfying the optional
// StreamFaultReporter interface.
//
// It exists because Stream's contract cannot express this. By the time the
// server says "I did not understand your frames", Stream has already returned
// a channel successfully — so a wrong guess in this file arrives as a stream
// that opens, meters a live microphone and transcribes nothing, which
// streamingVoiceTurn reports as ErrNoSpeech: a broken microphone. Asking
// afterwards is the only way the caller can tell the two apart.
func (p *openaiRealtimeSTT) StreamFault() error {
	p.faultMu.Lock()
	defer p.faultMu.Unlock()
	return p.fault
}

// setFault records a session-level failure, first one wins.
func (p *openaiRealtimeSTT) setFault(err error) {
	p.faultMu.Lock()
	if p.fault == nil {
		p.fault = err
	}
	p.faultMu.Unlock()
}

// NewOpenAIRealtimeSTT builds the streaming OpenAI transcription adapter.
func NewOpenAIRealtimeSTT(model, baseURL string, rc *RealtimeConfig) STTProvider {
	if model == "" {
		model = openaiRealtimeSTTModel
	}
	base, _ := NewOpenAISTT(model, baseURL).(*openaiSTT)
	return &openaiRealtimeSTT{openaiSTT: base, rc: rc}
}

// streamURL builds the WebSocket URL. coder/websocket reads an http(s) scheme
// as ws(s), so one base URL serves batch and stream.
func (p *openaiRealtimeSTT) streamURL() string {
	route := openaiRealtimeRoute
	if p.rc != nil && p.rc.Route != "" {
		route = p.rc.Route
	}
	if env := strings.TrimSpace(os.Getenv("HELIX_OPENAI_REALTIME_URL")); env != "" {
		// Whole-URL override, matching the HELIX_LLAMACPP_URL precedent: a
		// proxy or a local mock can be pointed at without touching config.
		return withQuery(env, p.realtimeQuery())
	}
	return withQuery(p.origin+route, p.realtimeQuery())
}

// realtimeQuery is the query string, with config overrides applied last.
func (p *openaiRealtimeSTT) realtimeQuery() map[string]string {
	q := map[string]string{"model": p.model}
	if p.rc != nil {
		for k, v := range p.rc.Query {
			q[k] = v
		}
	}
	return q
}

// withQuery appends a query map to a URL, preserving any existing string.
func withQuery(base string, q map[string]string) string {
	if len(q) == 0 {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	parts := make([]string, 0, len(q))
	for k, v := range q {
		parts = append(parts, k+"="+v)
	}
	// Sorted so the URL is stable across runs, which matters for a mock server
	// asserting on it.
	sortStrings(parts)
	return base + sep + strings.Join(parts, "&")
}

// sortStrings is a tiny insertion sort, avoiding a sort import for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// realtimeHeaders builds the dial headers.
func (p *openaiRealtimeSTT) realtimeHeaders(secret string) http.Header {
	h := http.Header{}
	if secret == "" {
		secret = p.key
	}
	h.Set("Authorization", "Bearer "+secret)
	// The OpenAI-Beta: realtime=v1 header is NOT sent. An earlier version set
	// it on the reasoning that it was harmless; the realtime guide is explicit
	// that it must go — "Remove the OpenAI-Beta: realtime=v1 header when
	// calling the GA interface" — so sending it is a request to be served by an
	// interface that is no longer current. Still addable through Headers below
	// for anyone pointing at an older deployment.
	if p.rc != nil {
		for k, v := range p.rc.Headers {
			h.Set(k, v)
		}
	}
	return h
}

// sessionFrame is the session-configuration frame.
//
// VERIFIED against OpenAI's realtime transcription guide, which publishes this
// shape naming gpt-live-transcribe directly. The first version of this
// function guessed, and guessed wrong in four places worth recording because
// they are the shape of the mistake rather than one typo:
//
//	type:                transcription_session.update  ->  session.update
//	session.type:        (absent)                      ->  "transcription"
//	audio format:        "input_audio_format": "pcm16" ->  session.audio.input.format
//	                                                        {type: audio/pcm, rate: N}
//	transcription model: "input_audio_transcription"   ->  session.audio.input.transcription
//
// Every one of them was a flat key where the real API nests under
// session.audio.input. A session built the old way would have been rejected —
// and rejected SILENTLY, since the socket still opens.
//
// The rate is sent rather than hardcoded: the guide's example says 24000
// because that is what its capture produced, while streamingVoiceTurn captures
// at 16 kHz. Copying the number out of an example is how you send a server
// audio at one rate and a promise about another.
//
// turn_detection stays null, which the guide also shows. streamingVoiceTurn
// already owns endpointing — a 3-second idle timer plus
// ConversationalMaxDuration — and two endpointers disagreeing is how a turn got
// cut mid-sentence before.
func (p *openaiRealtimeSTT) sessionFrame() ([]byte, error) {
	if p.rc != nil && len(p.rc.Session) > 0 {
		return p.rc.Session, nil // verbatim: the escape hatch
	}
	return json.Marshal(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format": map[string]any{
						"type": "audio/pcm",
						"rate": realtimeCaptureRate,
					},
					"transcription": map[string]any{
						"model": p.model,
					},
					"turn_detection": nil,
				},
			},
		},
	})
}

// realtimeCaptureRate is the rate streamingVoiceTurn captures at, and so the
// rate this session is told to expect. Named rather than repeated so the two
// cannot drift into disagreeing about what is on the wire.
const realtimeCaptureRate = 16000

// Stream consumes chunked audio and emits interim/final transcripts.
func (p *openaiRealtimeSTT) Stream(ctx context.Context, chunks <-chan AudioFormat) (<-chan Transcript, error) {
	if p.key == "" {
		// Before the dial: no socket should open without a credential.
		return nil, fmt.Errorf("%s: missing API key", p.name)
	}

	secret := ""
	if p.rc != nil && p.rc.SessionMode == "create_then_dial" {
		s, url, err := p.createSession(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: create session: %w", p.name, err)
		}
		secret = s
		if url != "" {
			return p.dialAndRun(ctx, url, secret, chunks)
		}
	}
	return p.dialAndRun(ctx, p.streamURL(), secret, chunks)
}

// createSession performs the POST-then-dial handshake, returning the ephemeral
// secret and (when the server supplies one) the URL to dial.
func (p *openaiRealtimeSTT) createSession(ctx context.Context) (string, string, error) {
	body, err := p.sessionFrame()
	if err != nil {
		return "", "", err
	}
	headers := map[string]string{"Authorization": "Bearer " + p.key}
	if p.rc != nil {
		for k, v := range p.rc.Headers {
			headers[k] = v
		}
	}
	route := openaiRealtimeRoute
	if p.rc != nil && p.rc.Route != "" {
		route = p.rc.Route
	}
	raw, err := sharedClient.DoRaw(ctx, http.MethodPost, p.origin+route,
		headers, "application/json", body)
	if err != nil {
		return "", "", err
	}
	var out struct {
		ClientSecret struct {
			Value string `json:"value"`
		} `json:"client_secret"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("session response: %w", err)
	}
	return out.ClientSecret.Value, out.URL, nil
}

// dialAndRun opens the socket and runs the writer/reader pair.
func (p *openaiRealtimeSTT) dialAndRun(ctx context.Context, url, secret string,
	chunks <-chan AudioFormat) (<-chan Transcript, error) {

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: p.realtimeHeaders(secret),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: dial: %w", p.name, err)
	}

	if frame, ferr := p.sessionFrame(); ferr == nil {
		// First frame, before any audio: the server has to be told the format
		// before it is sent any.
		if werr := conn.Write(ctx, websocket.MessageText, frame); werr != nil {
			_ = conn.Close(websocket.StatusInternalError, "session")
			return nil, fmt.Errorf("%s: session: %w", p.name, werr)
		}
	}

	out := make(chan Transcript, 16)
	binaryFraming := p.rc != nil && p.rc.AudioFraming == "binary"
	// A fresh session starts with no verdict: setFault keeps the FIRST fault,
	// so a previous session's must be cleared or it would be reported against
	// this one.
	p.faultMu.Lock()
	p.fault = nil
	p.faultMu.Unlock()

	// Writer. One concurrent writer alongside the reader is what
	// coder/websocket permits.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case clip, ok := <-chunks:
				if !ok {
					if p.rc.CommitsOnClose() {
						_ = conn.Write(ctx, websocket.MessageText,
							[]byte(`{"type":"input_audio_buffer.commit"}`))
					}
					return
				}
				pcm, cerr := toLinear16(clip)
				if cerr != nil {
					continue // best-effort, as the Deepgram writer is
				}
				if binaryFraming {
					if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
						return
					}
					continue
				}
				frame, merr := json.Marshal(map[string]string{
					"type":  "input_audio_buffer.append",
					"audio": base64.StdEncoding.EncodeToString(pcm),
				})
				if merr != nil {
					continue
				}
				if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
					return
				}
			}
		}
	}()

	go p.readLoop(ctx, conn, out)
	return out, nil
}

// readLoop accumulates segments and emits one utterance-final transcript.
//
// SEGMENTS, not the first final. The Deepgram adapter records what happens
// otherwise: finalising on the first per-segment final truncated commands at
// mid-sentence pauses, because streamingVoiceTurn returns the turn as soon as
// it sees IsFinal. So `.completed` appends to the accumulated text and is
// emitted as an INTERIM; the utterance-final goes out only when the stream
// ends.
func (p *openaiRealtimeSTT) readLoop(ctx context.Context, conn *websocket.Conn, out chan<- Transcript) {
	defer close(out)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	var segments []string
	var serverErr string
	emitted := false

	joined := func(partial string) string {
		all := append([]string{}, segments...)
		if strings.TrimSpace(partial) != "" {
			all = append(all, partial)
		}
		return strings.TrimSpace(strings.Join(all, " "))
	}
	emit := func(t Transcript) {
		select {
		case out <- t:
			emitted = true
		case <-ctx.Done():
		}
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			// Flush whatever was accumulated: a server-side close must never
			// eat a transcribed utterance.
			if text := joined(""); text != "" {
				emit(Transcript{Text: text, Provider: p.name, IsFinal: true})
				return
			}
			if serverErr != "" && !emitted {
				// Nothing transcribed AND the server complained. Far more
				// likely a wrong guess in this file than a quiet room, so
				// record it where the caller can act on it — see StreamFault.
				p.setFault(fmt.Errorf("%s: realtime session rejected the audio "+
					"(%s) — the wire format is inferred; set speech.stt.realtime "+
					"in the config to correct it", p.name, serverErr))
			}
			return
		}

		text, kind := parseRealtimeFrame(data)
		switch kind {
		case rtError:
			if serverErr == "" {
				serverErr = firstLine(text)
				if serverErr == "" {
					serverErr = "no detail given"
				}
			}
		case rtDelta:
			if text != "" {
				emit(Transcript{Text: joined(text), Provider: p.name, IsFinal: false})
			}
		case rtSegmentFinal:
			if text != "" {
				segments = append(segments, text)
				emit(Transcript{Text: joined(""), Provider: p.name, IsFinal: false})
			}
		case rtUtteranceEnd:
			if text := joined(""); text != "" {
				emit(Transcript{Text: text, Provider: p.name, IsFinal: true})
				return
			}
		}
	}
}

// firstLine bounds a server message to something printable on one line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return strings.TrimSpace(s)
}

// rtFrameKind classifies a realtime server frame.
type rtFrameKind int

const (
	rtOther rtFrameKind = iota
	rtDelta
	rtSegmentFinal
	rtUtteranceEnd
	rtError
)

// parseRealtimeFrame reads a server event TOLERANTLY.
//
// Classifying by the SHAPE of the type string rather than by exact event names
// is what keeps a wrong guess from being fatal: any `*.delta` is a partial,
// any `*.completed`/`*.done` is a segment final, anything unrecognised is
// ignored rather than treated as an error. The text is taken from whichever of
// delta/transcript/text is present, because the three spellings all appear in
// this family of APIs.
func parseRealtimeFrame(data []byte) (string, rtFrameKind) {
	var f struct {
		Type       string `json:"type"`
		Delta      string `json:"delta"`
		Transcript string `json:"transcript"`
		Text       string `json:"text"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return "", rtOther
	}
	text := f.Delta
	if text == "" {
		text = f.Transcript
	}
	if text == "" {
		text = f.Text
	}
	text = strings.TrimSpace(text)

	t := strings.ToLower(f.Type)
	switch {
	case t == "":
		return text, rtOther
	case strings.Contains(t, "error"):
		return text, rtError
	case strings.HasSuffix(t, ".delta"), strings.HasSuffix(t, ".partial"):
		return text, rtDelta
	case strings.HasSuffix(t, ".completed"), strings.HasSuffix(t, ".done"),
		strings.HasSuffix(t, ".final"):
		return text, rtSegmentFinal
	case strings.Contains(t, "speech_stopped"), strings.Contains(t, "utterance_end"):
		return text, rtUtteranceEnd
	default:
		return text, rtOther
	}
}

// ActiveRoute reports which realtime route and framing are in force, so
// /voice-status can show which of the inferred shapes is being used.
func (p *openaiRealtimeSTT) ActiveRoute() string {
	framing := "json_base64"
	mode := "direct"
	if p.rc != nil {
		if p.rc.AudioFraming != "" {
			framing = p.rc.AudioFraming
		}
		if p.rc.SessionMode != "" {
			mode = p.rc.SessionMode
		}
	}
	return fmt.Sprintf("%s (%s, %s)", p.streamURL(), mode, framing)
}
