// internal/live/events.go
// Purpose: the gpt-live-1 event vocabulary, as the live service states it.
//
// EVERY NAME HERE WAS ENUMERATED BY THE SERVER, not read from a guide. Sending
// an unknown `type` over the data channel answers with the complete list of
// supported values, and sending a known type with no body answers with the name
// of the next required field. That is how the whole of this file was obtained
// on 2026-09-11; §13 records the session.
//
// The parser is TOLERANT for the same reason adapter_openai_realtime_stt.go's
// is: it classifies on the `type` string and ignores what it does not
// recognise, so a vendor adding an event does not crash a session or, worse,
// silently stop one.
package live

import "encoding/json"

// Client event types. The server enumerates exactly these, and the two it
// enumerates only BEFORE session.started — session.start and
// session.input_audio.append — are deliberately absent: they are unreachable
// once a WebRTC session is running, and naming them here would invite the
// belief that audio can travel over the data channel. It cannot.
const (
	evtSessionUpdate     = "session.update"
	evtInputAudioMute    = "session.input_audio.mute"
	evtInputAudioUnmute  = "session.input_audio.unmute"
	evtInstructionsAppnd = "session.instructions.append"
	evtThinkingAppend    = "session.thinking.append"
	evtCommentaryAppend  = "session.commentary.append"
	evtResponseItemCreat = "response.item.create"
	evtSessionClose      = "session.close"
)

// Server event types observed across the probe sessions.
const (
	srvSessionStarted     = "session.started"
	srvInputTranscript    = "session.input_transcript.delta"
	srvOutputTranscript   = "session.output_transcript.delta"
	srvDelegationCreated  = "session.delegation.created"
	srvCommentaryAppended = "session.commentary.appended"
	srvInputAudioMuted    = "session.input_audio.muted"
	srvInputAudioUnmuted  = "session.input_audio.unmuted"
	srvUsageUpdated       = "session.usage.updated"
	srvError              = "error"
)

// clientEvent is one frame sent down the data channel.
//
// delegation_id and content are a pair: commentary, instructions and thinking
// all require both, and every one of those three refusals named `content` by
// that exact spelling — not `text`, which was the first guess and was wrong
// three times in a row.
type clientEvent struct {
	Type         string          `json:"type"`
	DelegationID string          `json:"delegation_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	Session      json.RawMessage `json:"session,omitempty"`
	Item         json.RawMessage `json:"item,omitempty"`
}

// serverEvent is the union of every server frame, parsed leniently.
//
// One struct rather than a type switch over many: every field is optional and
// absent fields decode to zero, so an event carrying a shape this build has
// never seen still parses and still reports its type. A strict per-type
// unmarshal would fail the whole frame on one unexpected key.
type serverEvent struct {
	Type    string `json:"type"`
	EventID string `json:"event_id"`

	// Delta, StartMs and EndMs carry the transcript stream.
	//
	// The two timestamps are STREAM time and lag wall clock by 1.5–2 s
	// (measured). They order events against each other correctly and are not a
	// clock; nothing in Helix may schedule on them.
	Delta   string `json:"delta"`
	StartMs int    `json:"start_ms"`
	EndMs   int    `json:"end_ms"`

	Delegation struct {
		ID     string `json:"id"`
		Target string `json:"target"`
		Type   string `json:"type"`
	} `json:"delegation"`

	Session struct {
		ID           string `json:"id"`
		Model        string `json:"model"`
		Status       string `json:"status"`
		Instructions string `json:"instructions"`
		Delegation   struct {
			Type string `json:"type"`
		} `json:"delegation"`
	} `json:"session"`

	Usage struct {
		Seconds int `json:"seconds"`
	} `json:"usage"`

	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
		Type    string `json:"type"`
	} `json:"error"`
}

// parseServerEvent decodes one frame. A frame that is not JSON at all returns
// ok=false; a frame that is JSON but unrecognised returns ok=true with whatever
// type string it carried, so the caller can log it rather than drop it silently.
func parseServerEvent(raw []byte) (serverEvent, bool) {
	var e serverEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return serverEvent{}, false
	}
	return e, true
}
