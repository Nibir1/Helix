// cmd/helix/wake_ack.go
// Purpose: say something out loud when a spoken word wakes Helix.
//
// WHY THIS IS NOT JUST A CHIME. Waking by voice is the one transition the user
// is, by construction, not watching: they spoke to a terminal from across the
// room. Everything the transition printed — "wake heard", the LIVE panel, the
// sense readout — is invisible to them, and the 880Hz ping says only "a sound
// happened", which is indistinguishable from every other ping the shell makes.
// So the acknowledgement has to be words, and it has to arrive before the
// recorder opens or the user starts talking into a microphone that is not yet
// listening.
//
// WHY IT VARIES. A fixed string read out on every wake stops being information
// after the third time — it becomes the noise you learn to talk over, and then
// you can no longer tell a real wake from a remembered one. Rotating a small
// set keeps it audible. The set is deliberately small and short: this speech
// sits between the user deciding to talk and Helix being able to hear them, so
// every extra syllable is latency in the one place latency is felt.
//
// WHY NONE OF THEM MENTION A PHRASE. The default energy engine wakes on speech
// onset and does not know what was said (ADR-002). "I heard 'hey helix'" would
// be the fifth place in this codebase promising what the detector cannot
// deliver — see standbyHint, wakeHeardDetail, blackBoxWakeLine, wakeBannerLines.
package main

import (
	"math/rand/v2"
	"sync"

	"helix/internal/speech"
)

// wakeAcknowledgements are the spoken "I'm awake" lines.
//
// Presence, not comprehension: each one says Helix is listening and hands the
// turn back, which is all that is actually known at this point. Kept under
// about five syllables so the acknowledgement does not become the wait.
var wakeAcknowledgements = []string{
	"I'm listening.",
	"Go ahead.",
	"I'm here.",
	"Yes?",
	"Ready.",
	"Go on.",
	"Right here.",
	"What do you need?",
	"Listening.",
	"All ears.",
}

// wakeAckState remembers the last line so the picker never repeats immediately.
var wakeAckState struct {
	mu   sync.Mutex
	last int
}

// pickWakeAcknowledgement returns a line to speak, never the one just used.
//
// Uniform random would repeat about one wake in ten, and a repeat is the single
// most noticeable failure mode here: hearing the same four words twice running
// reads as a stutter or a double trigger, which is exactly the doubt this
// feature exists to remove. Excluding the previous choice costs nothing and
// makes that impossible.
func pickWakeAcknowledgement() string {
	if len(wakeAcknowledgements) == 0 {
		return ""
	}
	if len(wakeAcknowledgements) == 1 {
		return wakeAcknowledgements[0]
	}

	wakeAckState.mu.Lock()
	defer wakeAckState.mu.Unlock()

	// Draw from the set minus the previous pick, then map back — one call, no
	// retry loop that could in principle spin.
	i := rand.IntN(len(wakeAcknowledgements) - 1)
	if i >= wakeAckState.last {
		i++
	}
	wakeAckState.last = i
	return wakeAcknowledgements[i]
}

// speakWakeAcknowledgement says one line, blocking until it has finished.
//
// MUTED MEANS MUTED, and that is a deliberate exception to this codebase's
// bookkeeping rule. speakDirect exists because an acknowledgement, a refusal or
// a clarification is not a REPLY, so /blackbox tts off should not suppress it —
// the user spoke to a terminal they may not be looking at, and silence there is
// the real failure. That reasoning holds for a refusal, which carries
// information the user cannot get any other way. It does not hold here: the
// only thing this line conveys is "I am awake", and the ready chime already
// conveys it to someone who has explicitly asked Helix to stop talking.
// Speaking over an explicit mute to announce that we can speak would be the
// most annoying possible reading of the rule. So the gate is checked, and
// speakDirect is still the transport underneath it.
//
// Blocking is load-bearing, not incidental. Capture is half-duplex, and
// voiceTurn opens the recorder immediately after this returns; if the
// acknowledgement were still playing, voiceTurn's StopSpeaking would cut it off
// mid-word AND the tail would land in the recording. Returning only when the
// speaker is done is what keeps both from happening.
func speakWakeAcknowledgement() {
	if !speech.TTSEnabled() {
		return
	}
	if line := pickWakeAcknowledgement(); line != "" {
		speakDirect(line)
	}
}
