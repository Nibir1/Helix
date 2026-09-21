// internal/live/pcmqueue.go
// Purpose: the model's decoded speech, as an io.ReadCloser the existing
// playback stack can consume.
//
// WHY A BOUNDED, LOSSY QUEUE AND NOT AN io.Pipe. An io.Pipe blocks the writer
// until someone reads, and the writer here is the RTP reader — the goroutine
// that must never stall, because stalling it stops draining the socket and the
// jitter buffer grows until the connection is unusable. A full-duplex stream
// also never ends on its own: it carries audio for the whole conversation,
// speech and silence alike, at a fixed 50 packets a second.
//
// So the queue drops the OLDEST audio when it overflows. In a live conversation
// that is the right loss: audio the speaker has already moved past is worth
// less than audio arriving now, and the alternative — dropping the newest —
// produces a stream that is permanently a second behind and never catches up.
package live

import (
	"errors"
	"io"
	"sync"
)

// pcmQueueMaxBytes bounds the buffer at ~2 s of 48 kHz mono 16-bit audio.
//
// Two seconds rather than a tighter figure because audio.PlaySpeechStream
// pre-buffers 250 ms and then pulls in 8 KB reads; anything under about half a
// second would drop on ordinary scheduling jitter rather than on a real stall.
const pcmQueueMaxBytes = AudioSampleRate * 2 * 2

// pcmQueue is a bounded FIFO of PCM bytes with blocking reads.
type pcmQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newPCMQueue() *pcmQueue {
	q := &pcmQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// write appends samples, dropping the oldest bytes on overflow.
func (q *pcmQueue) write(pcm []int16) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	for _, s := range pcm {
		q.buf = append(q.buf, byte(uint16(s)), byte(uint16(s)>>8))
	}
	if len(q.buf) > pcmQueueMaxBytes {
		// Drop a whole number of 20 ms FRAMES, not an arbitrary byte count.
		//
		// Sample alignment alone is not the property worth having and would not
		// need code — write appends two bytes per sample, so the buffer length
		// is always even and a byte-count drop is always even too. (That was
		// found by mutating the first version of this: removing its odd-offset
		// guard changed nothing, because the guard could not fire. §9 rule 8.)
		// The property that DOES need code is the frame boundary: a decoder
		// handed a partial frame produces an audible click at the splice, and
		// an overflow already sounds like a jump without adding one.
		const frameBytes = FrameSamples * 2
		drop := len(q.buf) - pcmQueueMaxBytes
		if rem := drop % frameBytes; rem != 0 {
			drop += frameBytes - rem
		}
		if drop > len(q.buf) {
			drop = len(q.buf)
		}
		q.buf = q.buf[drop:]
	}
	q.cond.Broadcast()
}

// Read blocks until bytes are available or the queue is closed.
func (q *pcmQueue) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, q.buf)
	q.buf = q.buf[n:]
	return n, nil
}

// Close unblocks every reader and discards what is queued.
func (q *pcmQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("live: audio queue already closed")
	}
	q.closed = true
	q.buf = nil
	q.cond.Broadcast()
	return nil
}
