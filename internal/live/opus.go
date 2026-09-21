// internal/live/opus.go
// Purpose: Opus encode and decode through libopus, loaded at runtime with
// purego so CGO_ENABLED=0 keeps working (§12 guardrail 8).
//
// WHY AN ENCODER IS NOT OPTIONAL, and how that was settled rather than assumed.
// The obvious hope was a base64 side door on the data channel, the way
// adapter_openai_realtime_stt.go sends input_audio_buffer.append over its
// WebSocket. `session.input_audio.append` does exist — it is in the vocabulary
// the server enumerates when it rejects an unknown event type — but it is in
// that list ONLY until `session.started` arrives, and gone from every
// enumeration after. Audio therefore reaches gpt-live-1 as RTP or not at all,
// and RTP carries Opus. Measured, 2026-09-11; see §13.
//
// WHY purego RATHER THAN cgo. ADR-003's reasoning applied to a library instead
// of a binary: scripts/build.sh cross-compiles five platforms from one machine,
// and a cgo dependency costs a C toolchain per target plus a glibc pin. purego
// is already an indirect dependency through oto, so the playback path already
// calls C without cgo, and opus_encode's signature — ints and pointers, no
// structs by value, no callbacks — is the case purego handles cleanly.
//
// WHAT THIS COSTS, stated because it is a real runtime dependency: libopus has
// to be installed (`brew install opus`, `apt install libopus0`). That matches
// the existing posture — Helix already requires sox or ffmpeg to capture — and
// the failure is a named, actionable error rather than a silent one.
package live

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// opusApplicationVOIP is OPUS_APPLICATION_VOIP from opus_defines.h. VOIP rather
// than AUDIO: the input is one person talking into a laptop microphone, which
// is exactly what its speech model is tuned for.
const opusApplicationVOIP = 2048

// opusMaxPacket bounds one encoded frame. 4000 is the figure libopus's own
// documentation uses for the encode buffer; a 20 ms speech frame is two orders
// of magnitude smaller.
const opusMaxPacket = 4000

// ErrNoOpus is returned when libopus cannot be found. Distinguished from every
// other failure because it is the ONE that has a user action attached, and the
// caller turns it into install instructions.
var ErrNoOpus = errors.New("libopus not found")

type opusLib struct {
	encoderCreate  func(fs, channels, application int32, errp unsafe.Pointer) uintptr
	encode         func(st uintptr, pcm unsafe.Pointer, frameSize int32, data unsafe.Pointer, maxBytes int32) int32
	encoderDestroy func(st uintptr)
	decoderCreate  func(fs, channels int32, errp unsafe.Pointer) uintptr
	decode         func(st uintptr, data unsafe.Pointer, length int32, pcm unsafe.Pointer, frameSize, decodeFEC int32) int32
	decoderDestroy func(st uintptr)
	strerror       func(code int32) string
	name           string
}

var (
	opusOnce sync.Once
	opusOnly *opusLib
	opusErr  error
)

// loadOpus resolves libopus once per process.
//
// Once rather than per-session: Dlopen on an already-loaded library is cheap
// but RegisterLibFunc allocates trampolines, and a session that reconnects
// after a network blip should not leak one set per attempt.
func loadOpus() (*opusLib, error) {
	opusOnce.Do(func() {
		for _, name := range opusLibNames {
			h, err := openOpusLibrary(name)
			if err != nil {
				continue
			}
			l := &opusLib{name: name}
			purego.RegisterLibFunc(&l.encoderCreate, h, "opus_encoder_create")
			purego.RegisterLibFunc(&l.encode, h, "opus_encode")
			purego.RegisterLibFunc(&l.encoderDestroy, h, "opus_encoder_destroy")
			purego.RegisterLibFunc(&l.decoderCreate, h, "opus_decoder_create")
			purego.RegisterLibFunc(&l.decode, h, "opus_decode")
			purego.RegisterLibFunc(&l.decoderDestroy, h, "opus_decoder_destroy")
			purego.RegisterLibFunc(&l.strerror, h, "opus_strerror")
			opusOnly = l
			return
		}
		opusErr = fmt.Errorf("%w: tried %v — %s", ErrNoOpus, opusLibNames, opusInstallHint)
	})
	return opusOnly, opusErr
}

// OpusAvailable reports whether libopus can be loaded, without constructing a
// codec. Used by the preflight so "go live" fails at the door with an
// actionable message instead of halfway through a paid session.
func OpusAvailable() error {
	_, err := loadOpus()
	return err
}

// OpusLibraryName returns the soname or path libopus was loaded from, for
// /blackbox status. Empty when it is not loaded.
func OpusLibraryName() string {
	l, err := loadOpus()
	if err != nil {
		return ""
	}
	return l.name
}

// Encoder turns 16-bit PCM frames into Opus packets. Not safe for concurrent
// use; the session owns exactly one and drives it from its pacer goroutine.
type Encoder struct {
	lib *opusLib
	st  uintptr
	buf []byte
}

// NewEncoder creates an encoder for the given rate and channel count.
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	lib, err := loadOpus()
	if err != nil {
		return nil, err
	}
	var code int32
	st := lib.encoderCreate(int32(sampleRate), int32(channels), opusApplicationVOIP, unsafe.Pointer(&code))
	if st == 0 || code != 0 {
		return nil, fmt.Errorf("opus_encoder_create(%d,%d): %s", sampleRate, channels, lib.strerror(code))
	}
	return &Encoder{lib: lib, st: st, buf: make([]byte, opusMaxPacket)}, nil
}

// Encode encodes one frame of frameSize samples per channel.
//
// The returned slice is a copy. Returning e.buf directly would be free and
// would be a bug: pion queues the sample and the next call overwrites it.
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	if e == nil || e.st == 0 {
		return nil, errors.New("opus: encoder is closed")
	}
	if len(pcm) < frameSize {
		return nil, fmt.Errorf("opus: frame is %d samples, need %d", len(pcm), frameSize)
	}
	n := e.lib.encode(e.st, unsafe.Pointer(&pcm[0]), int32(frameSize), unsafe.Pointer(&e.buf[0]), int32(len(e.buf)))
	runtime.KeepAlive(pcm)
	if n < 0 {
		return nil, fmt.Errorf("opus_encode: %s", e.lib.strerror(n))
	}
	out := make([]byte, n)
	copy(out, e.buf[:n])
	return out, nil
}

// Close releases the encoder. Safe to call twice.
func (e *Encoder) Close() {
	if e != nil && e.st != 0 {
		e.lib.encoderDestroy(e.st)
		e.st = 0
	}
}

// Decoder turns Opus packets back into 16-bit PCM. Not safe for concurrent use.
type Decoder struct {
	lib      *opusLib
	st       uintptr
	channels int
	buf      []int16
}

// NewDecoder creates a decoder for the given rate and channel count.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	lib, err := loadOpus()
	if err != nil {
		return nil, err
	}
	var code int32
	st := lib.decoderCreate(int32(sampleRate), int32(channels), unsafe.Pointer(&code))
	if st == 0 || code != 0 {
		return nil, fmt.Errorf("opus_decoder_create(%d,%d): %s", sampleRate, channels, lib.strerror(code))
	}
	// 120 ms is the longest frame Opus can carry, so this buffer can never be
	// the reason a packet fails to decode.
	return &Decoder{lib: lib, st: st, channels: channels, buf: make([]int16, sampleRate/1000*120*channels)}, nil
}

// Decode decodes one packet and returns the PCM samples (interleaved).
func (d *Decoder) Decode(packet []byte) ([]int16, error) {
	if d == nil || d.st == 0 {
		return nil, errors.New("opus: decoder is closed")
	}
	if len(packet) == 0 {
		return nil, errors.New("opus: empty packet")
	}
	maxFrame := len(d.buf) / d.channels
	n := d.lib.decode(d.st, unsafe.Pointer(&packet[0]), int32(len(packet)),
		unsafe.Pointer(&d.buf[0]), int32(maxFrame), 0)
	runtime.KeepAlive(packet)
	if n < 0 {
		return nil, fmt.Errorf("opus_decode: %s", d.lib.strerror(n))
	}
	out := make([]int16, int(n)*d.channels)
	copy(out, d.buf[:len(out)])
	return out, nil
}

// Close releases the decoder. Safe to call twice.
func (d *Decoder) Close() {
	if d != nil && d.st != 0 {
		d.lib.decoderDestroy(d.st)
		d.st = 0
	}
}
