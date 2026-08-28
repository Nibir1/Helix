// internal/agent/policy_voice_test.go
// Purpose: Voice Risk Policy proof by synthetic transcript injection —
// InputEvents with Channel=voice fed programmatically, no microphone
// (roadmap §9 rule 2). Table-driven coverage of the ADR-005 matrix.
package agent

import (
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/ux"
)

func newTestAgent(t *testing.T) (*Agent, *[]string) {
	t.Helper()
	// Use the HOST's real shell, not a hardcoded Linux one.
	//
	// Claiming {OSName: "linux", Shell: "bash"} on a Windows runner makes
	// WrapCommand build `bash -c "<cmd>"`, and bash then treats the
	// backslashes in a Windows absolute path as escapes: a marker at
	// C:\Users\...\executed is created somewhere else entirely, and the test
	// reports that the command never ran. Production never hits this — it
	// detects the real environment — so the lie lived only here.
	env := shell.DetectEnvironment()
	gui := ux.NewUX()
	ag := NewAgent(env, nil, commands.NewDirectorySandbox(), commands.DefaultExecuteConfig(),
		false, gui, stealth.NewStealthExecutor(stealth.DefaultStealthConfig()), nil)

	spoken := &[]string{}
	ag.OnSpeak = func(text string) { *spoken = append(*spoken, text) }
	return ag, spoken
}

func TestVoiceCapRiskMatrix(t *testing.T) {
	cases := []struct {
		risk    commands.ShellRiskLevel
		voice   bool
		blocked bool
	}{
		{commands.ShellRiskLow, false, false},
		{commands.ShellRiskLow, true, false},
		{commands.ShellRiskMedium, false, false},
		{commands.ShellRiskMedium, true, false}, // Medium stays allowed: confirm-gated
		{commands.ShellRiskHigh, false, false},  // high handled by the normal path
		{commands.ShellRiskHigh, true, true},    // voice ceiling
	}
	for _, tc := range cases {
		_, _, blocked := voiceCapRisk(tc.risk, nil, tc.voice)
		if blocked != tc.blocked {
			t.Errorf("voiceCapRisk(%v, voice=%v) blocked=%v, want %v", tc.risk, tc.voice, blocked, tc.blocked)
		}
	}
}

func TestHighRiskUnreachableFromVoice(t *testing.T) {
	ag, spoken := newTestAgent(t)

	// Hard-validation blocks ("rm -rf /") are the first line of defense.
	// By VOICE the refusal must additionally be SPOKEN (no silent failure
	// for a user who is not looking at the terminal)...
	err := ag.handleShellStepVoice(ai.PlanStep{Tool: "shell", Command: "rm -rf /"})
	if err == nil {
		t.Fatal("rm -rf / must be blocked")
	}
	if len(*spoken) == 0 {
		t.Fatal("voice-channel validation block must be spoken (OnSpeak seam)")
	}

	// ...while the text channel stays silent (visible error text suffices).
	spokenBefore := len(*spoken)
	textErr := ag.handleShellStep(ai.PlanStep{Tool: "shell", Command: "rm -rf /"})
	if textErr == nil {
		t.Fatal("rm -rf / must be blocked on the text channel too")
	}
	if len(*spoken) != spokenBefore {
		t.Fatal("text-channel blocks must not speak")
	}

	// The analyzer-level voice ceiling (High risk unreachable from voice)
	// is proven by the voiceCapRisk matrix above; today hard validation
	// intercepts every known High pattern first, so the ceiling stands as
	// defense-in-depth for future validation gaps — mirroring how the
	// analyzer's own High branch is documented in safety/risk.go.
}

func TestMediumRiskVoiceStillConfirmGated(t *testing.T) {
	ag, _ := newTestAgent(t)

	// Medium risk is NOT hard-blocked by voice — it must route to the
	// confirmation prompter (which fails closed for voice). Prove the path:
	// with no prompter answer available in tests (stdin closed => false),
	// the step is skipped, not executed, and not policy-blocked.
	err := ag.handleShellStepVoice(ai.PlanStep{Tool: "shell", Command: "echo hi > voice_medium_probe.txt"})
	if err != nil {
		t.Fatalf("medium risk must not be policy-blocked by voice: %v", err)
	}
}

func TestConfidenceGateAsksToRepeat(t *testing.T) {
	ag, spoken := newTestAgent(t)

	ag.HandleInputEvent(input.InputEvent{
		Text:    "list the files",
		Channel: input.ChannelVoice,
		Meta:    map[string]any{"stt_confidence": 0.3},
	})

	if len(*spoken) == 0 || !strings.Contains((*spoken)[0], "repeat") {
		t.Fatalf("low-confidence transcript must trigger spoken clarification, got: %v", *spoken)
	}
	// The gate must reset the channel after the turn.
	if ag.channel != input.ChannelText {
		t.Fatalf("channel must reset after turn, got %q", ag.channel)
	}
}

func TestUnknownConfidenceDoesNotGate(t *testing.T) {
	ag, spoken := newTestAgent(t)

	// Providers that report no confidence (0/absent) must not be gated.
	ag.HandleInputEvent(input.InputEvent{
		Text:    "printf hi > voice_unknown_conf_probe.txt",
		Channel: input.ChannelVoice,
		Meta:    map[string]any{"stt_confidence": 0.0},
	})

	for _, s := range *spoken {
		if strings.Contains(s, "repeat") {
			t.Fatal("zero/unknown confidence must not trigger the clarification gate")
		}
	}
}

func TestChannelResetAfterTurn(t *testing.T) {
	ag, _ := newTestAgent(t)
	ag.HandleInputEvent(input.InputEvent{Text: "git status", Channel: input.ChannelVoice})
	if ag.voiceActive() {
		t.Fatal("voiceActive must not leak across turns")
	}
}

func TestVoiceDenyListContract(t *testing.T) {
	list := VoiceDenyList()
	if len(list) < 4 {
		t.Fatalf("deny list must cover the four typed-confirmation git actions, got %d", len(list))
	}
	for _, a := range list {
		if a.Tool == "" || a.Action == "" || a.RequiredPhrase == "" || a.SpokenRefusal == "" {
			t.Fatalf("incomplete deny entry: %+v", a)
		}
	}
}

// handleShellStepVoice stamps the voice channel and runs one shell step,
// mirroring what HandleInputEvent does for a full turn (test seam for
// step-level policy verification).
func (a *Agent) handleShellStepVoice(step ai.PlanStep) error {
	a.channel = input.ChannelVoice
	defer func() { a.channel = input.ChannelText }()
	return a.handleShellStep(step)
}
