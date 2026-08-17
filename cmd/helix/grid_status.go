// cmd/helix/grid_status.go
// Purpose: the per-turn "GRID STATUS" line, derived from real state.
//
// It used to be unconditional: `gui.PrintSuccess("Helix :: GRID STATUS :: CLEAR")`
// after every turn, so the QA session printed CLEAR while whisper-local was
// unreachable and TTS was 385ms over budget. A status line that is always green
// carries no information, and worse, it actively contradicts the degradation the
// rest of the stack was reporting honestly.
//
// The constraint that shapes the design: this runs in the interactive hot loop,
// once per turn, so it may NOT probe anything. Every signal here is state the
// subsystems already recorded as a side effect of doing their work — the speech
// registry's last chain outcomes (ChainHealth), the LLM circuit breaker's
// degraded flag (ADR-016), and the offline-mode toggle. The decision itself is a
// pure function of those signals so it can be tested without a voice stack.
package main

import (
	"strings"

	"helix/internal/ai"
	"helix/internal/speech"
)

// gridSignals is the cheap, already-available state the status line reads.
type gridSignals struct {
	// STT/TTS are the most recent speech failover-chain outcomes. Zero values
	// mean "not used this session", which is not degradation.
	STT speech.ChainHealth
	TTS speech.ChainHealth

	// Offline is speech local-first degradation (P4.10).
	Offline bool

	// LocalLLM is true while the cloud→local brain failover is engaged (P11.2).
	LocalLLM bool
}

// gridStatus is the rendered verdict for one turn.
type gridStatus struct {
	// Degraded selects the warning color and the DEGRADED wording.
	Degraded bool

	// Line is the whole message, already formatted — always exactly one line.
	Line string
}

// gridStatusPrefix is the branding the line has always carried; kept identical
// so the change reads as "the verdict got honest", not "the UI moved".
const gridStatusPrefix = "Helix :: GRID STATUS :: "

// evaluateGridStatus turns the collected signals into the status line.
//
// Reason order is worst-first: a failed chain matters more than a fallback, and
// the brain being local matters more than which microphone answered.
//
// Args:
//   - s: the signals sampled at the end of a turn.
//
// Returns: the verdict and the exact line to print.
// Complexity: O(number of failed providers).
func evaluateGridStatus(s gridSignals) gridStatus {
	var reasons []string

	if s.LocalLLM {
		reasons = append(reasons, "brain: local model (cloud unreachable)")
	}
	if s.Offline {
		reasons = append(reasons, "offline mode: local providers first")
	}
	if r := s.STT.Reason(); r != "" {
		reasons = append(reasons, "stt "+r)
	}
	if r := s.TTS.Reason(); r != "" {
		reasons = append(reasons, "tts "+r)
	}

	if len(reasons) == 0 {
		return gridStatus{Line: gridStatusPrefix + "CLEAR"}
	}
	return gridStatus{
		Degraded: true,
		Line:     gridStatusPrefix + "DEGRADED (" + strings.Join(reasons, "; ") + ")",
	}
}

// currentGridSignals samples the live subsystems. Pure reads of state they
// already hold — no network, no health checks, no audio device.
func currentGridSignals() gridSignals {
	sig := gridSignals{LocalLLM: ai.LocalFallbackActive()}
	if reg := speech.Default(); reg != nil {
		sig.STT = reg.LastSTTHealth()
		sig.TTS = reg.LastTTSHealth()
		sig.Offline = reg.Offline()
	}
	return sig
}
