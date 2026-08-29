// cmd/helix/voice_presets.go
//
// Purpose: BlackBox P9.7 — recommended chain presets for /blackbox setup.
//
// The wizard's provider tables are honest and complete, which is exactly the
// problem they create: choosing a voice chain means reading eleven rows across
// two tables, understanding that a fallback is a separate decision, and knowing
// that a "free" local row costs a running sidecar instead of money. Someone who
// just wants Helix to talk should not have to become a speech-provider analyst
// first, and ADR-011/ADR-012 already worked out the right answers.
//
// A preset is therefore a pre-filled ANSWER, not a new mechanism. It selects
// primary + fallback for both directions and then hands straight back to the
// existing path: keys are still requested and verified, sidecar ports are still
// assigned and probed, and the chain is still verified before the wizard claims
// success. Anything a preset skipped would be a second code path that could
// drift from the manual one — the class of bug this file's neighbours are full
// of — so it skips nothing.
package main

import (
	"fmt"
	"strings"

	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/speech"
)

// speechPreset is one recommended chain.
type speechPreset struct {
	Name string
	Note string // why you would pick this one
	Tag  string // a short precondition marker, e.g. "needs a GPU"

	STTProvider  string
	STTModel     string
	STTFallbacks []string

	TTSProvider  string
	TTSModel     string
	TTSVoice     string
	TTSFallbacks []string

	// TTSContextTurns turns on conversational conditioning for a voice whose
	// point IS the conditioning.
	//
	// Off everywhere by default, and rightly: retaining recent turns means
	// holding captured AUDIO in memory beyond the turn that produced it, which
	// is a privacy-relevant change (threat V5b). But a preset that advertises
	// "conversational prosody" and ships it disabled delivers a voice
	// indistinguishable from the fallback it was chosen over — which is what
	// happened: CSM was built, downloaded, started, and then sounded, in the
	// user's words, like "stale walkie-talkie style just like other voice
	// modes".
	//
	// Choosing this chain IS the request for that behaviour. It is announced
	// when applied rather than switched on quietly.
	TTSContextTurns int
}

// speechPresets returns the recommended chains, cheapest first.
//
// Every entry is the conclusion of an ADR rather than a fresh opinion:
// ADR-011 picked Groq turbo + gpt-4o-mini-tts as cheapest-good and Deepgram
// Nova-3 + Aura-2 as lowest-latency; ADR-012 picked the whisper.cpp + Piper
// local chain. The models are the same identifiers pricing.json carries, and a
// test pins them to it so a catalog rename cannot leave a preset pointing at a
// model no provider serves.
//
// Two deliberate choices worth stating. Every cloud preset carries a LOCAL
// fallback, because the point of a fallback is surviving the thing most likely
// to break — the network — and a second cloud vendor does not. And the local
// preset carries NO fallback: adding a cloud one would quietly undo the reason
// somebody chose "private", which is the one preset where a surprise would be a
// betrayal rather than an inconvenience.
func speechPresets() []speechPreset {
	return []speechPreset{
		{
			Name:         "Cheapest cloud",
			Note:         "large-model accuracy at ~$0.04/hr · ADR-011 recommendation",
			STTProvider:  "groq",
			STTModel:     "whisper-large-v3-turbo",
			STTFallbacks: []string{"whisper-local"},
			TTSProvider:  "openai",
			TTSModel:     "gpt-4o-mini-tts",
			TTSVoice:     "alloy",
			TTSFallbacks: []string{"piper-local"},
		},
		{
			Name:         "Lowest latency",
			Note:         "streaming partials and ~300ms first byte · needs a Deepgram key",
			STTProvider:  "deepgram",
			STTModel:     "nova-3",
			STTFallbacks: []string{"whisper-local"},
			TTSProvider:  "deepgram",
			TTSModel:     "aura-2-thalia-en",
			TTSVoice:     "aura-2-thalia-en",
			TTSFallbacks: []string{"piper-local"},
		},
		{
			// The precondition rides the Tag rather than the label: the menu
			// sizes its description column to the LONGEST label, so a
			// parenthetical here indents every other preset's note to pay for
			// this one. Tags are also where "needs a key" already lives, so the
			// two preconditions read the same way.
			Name: "Most natural, local",
			Tag:  "needs a GPU",
			Note: "Sesame CSM-1B — conversational prosody, nothing leaves the machine · ~8GB VRAM",
			// whisper-local ears, CSM voice, and piper as the fallback that
			// keeps the chain usable when CSM is too slow on this hardware —
			// which is the expected outcome on any machine without a GPU candle
			// can use, notably Intel Macs.
			STTProvider:  "whisper-local",
			STTFallbacks: nil,
			TTSProvider:  "csm-local",
			TTSVoice:     "0",
			TTSFallbacks: []string{"piper-local"},
			// Four turns is what makes this chain what its name says. Without
			// it CSM synthesizes each reply cold and sounds like any other
			// voice — the feature the preset is named for is the conditioning,
			// not the model file.
			TTSContextTurns: 4,
		},
		{
			Name: "Fully local / private",
			Note: "no key, no per-call cost, nothing leaves the machine · no docker",
			// No fallback in either direction: see the function comment.
			STTProvider: "whisper-local",
			TTSProvider: "piper-local",
		},
	}
}

// presetMenuItems renders the presets plus the manual escape hatch.
func presetMenuItems(presets []speechPreset) []shell.MenuItem {
	items := make([]shell.MenuItem, 0, len(presets)+1)
	for i, p := range presets {
		it := shell.MenuItem{Label: p.Name, Note: p.Note, Tag: p.Tag}
		if it.Tag == "" && p.needsKey() {
			it.Tag = "needs a key"
		}
		// The recommendation is COMBINED with the precondition rather than
		// replacing it. Assigning "recommended" first and letting needsKey
		// overwrite it meant the recommended preset never actually said so —
		// it rendered "needs a key" in the endorsement colour, so the top entry
		// looked like a warning painted green and the recommendation was
		// invisible on every menu Helix has ever drawn.
		if i == 0 {
			it.Good = true
			if it.Tag == "" {
				it.Tag = "recommended"
			} else {
				it.Tag = "recommended · " + it.Tag
			}
		}
		items = append(items, it)
	}
	return append(items, shell.MenuItem{
		Label: "Choose manually",
		Note:  "see every provider with prices and pick your own",
	})
}

// needsKey reports whether the preset uses any cloud provider.
func (p speechPreset) needsKey() bool {
	return !isLocalSpeechProvider(p.STTProvider) || !isLocalSpeechProvider(p.TTSProvider)
}

// isLocalSpeechProvider reports whether a provider name is a local sidecar.
// Derived from the pricing catalog rather than a second hardcoded list, so a
// new local adapter is classified correctly the moment it is catalogued.
func isLocalSpeechProvider(name string) bool {
	if name == "" {
		return false
	}
	catalog, err := speech.LoadMergedCatalog()
	if err != nil {
		return strings.HasSuffix(name, "-local")
	}
	for _, e := range catalog {
		if e.Provider == name {
			return e.Local
		}
	}
	return strings.HasSuffix(name, "-local")
}

// presetSummary describes a chain in one line, for the confirmation.
func (p speechPreset) presetSummary() string {
	stt := p.STTProvider
	if len(p.STTFallbacks) > 0 {
		stt += " → " + strings.Join(p.STTFallbacks, " → ")
	}
	tts := p.TTSProvider
	if len(p.TTSFallbacks) > 0 {
		tts += " → " + strings.Join(p.TTSFallbacks, " → ")
	}
	return fmt.Sprintf("hears you with %s · answers with %s", stt, tts)
}

// availablePresets drops presets whose providers this build does not register,
// so the menu can never offer a chain that cannot be selected.
func availablePresets(sttNames, ttsNames []string) []speechPreset {
	out := make([]speechPreset, 0, len(speechPresets()))
	for _, p := range speechPresets() {
		if !validName(sttNames, p.STTProvider) || !validName(ttsNames, p.TTSProvider) {
			continue
		}
		p.STTFallbacks = keepRegistered(p.STTFallbacks, sttNames)
		p.TTSFallbacks = keepRegistered(p.TTSFallbacks, ttsNames)
		out = append(out, p)
	}
	return out
}

// keepRegistered filters a fallback list down to registered providers.
func keepRegistered(names, registered []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if validName(registered, n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appliedPreset carries a preset expanded into config sections.
type appliedPreset struct {
	stt config.SpeechSTTConfig
	tts config.SpeechTTSConfig
}

// offerSpeechPresets shows the recommended chains and, if one is chosen,
// prepares it fully — keys requested and verified, sidecar ports assigned and
// probed — returning the config sections ready to commit.
//
// The second return value is false when the user wants the manual tables, which
// is also what an unparseable answer means: a wizard should fall through to
// MORE choice, never less.
func offerSpeechPresets(reg *speech.Registry, catalog []speech.PricingEntry) (appliedPreset, bool) {
	presets := availablePresets(reg.STTNames(), reg.TTSNames())
	if len(presets) == 0 {
		return appliedPreset{}, false
	}

	fmt.Println(shell.PanelTitle("recommended chains"))
	for _, l := range shell.PanelWrap(
		"one pick configures both directions, with a local fallback so a dropped "+
			"network does not take your voice with it", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	for _, l := range shell.Menu(presetMenuItems(presets)) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())

	choice := askNumber(shell.Prompt("pick a chain", "choose manually"), len(presets)+1)
	// The manual entry is the last item, and 0 (blank) means the same thing.
	if choice <= 0 || choice > len(presets) {
		return appliedPreset{}, false
	}

	p := presets[choice-1]
	fmt.Println(shell.Hint(p.Name + " — " + p.presetSummary()))
	return p.apply(catalog), true
}

// apply prepares every provider in the preset and returns the config sections.
//
// This walks the SAME per-provider preparation the manual path uses
// (autoAssignSidecarPort → prepareSpeechProvider), for the primary and every
// fallback. A preset that skipped it would produce a chain that looks configured
// and cannot serve a request — which is the exact failure mode the wizard's
// verify step was added to catch.
func (p speechPreset) apply(catalog []speech.PricingEntry) appliedPreset {
	var out appliedPreset

	if p.STTProvider != "" {
		out.stt.Provider = p.STTProvider
		out.stt.Model = presetAPIModel(catalog, "stt", p.STTProvider, p.STTModel)
		out.stt.BaseURL = autoAssignSidecarPort("stt", p.STTProvider, cfg.Speech.STT.BaseURL)
		prepareSpeechProvider("stt", p.STTProvider)
		out.stt.Fallbacks = p.STTFallbacks
		for _, name := range out.stt.Fallbacks {
			autoAssignSidecarPort("stt", name, "")
			prepareSpeechProvider("stt", name)
		}
	}

	if p.TTSProvider != "" {
		out.tts.Provider = p.TTSProvider
		out.tts.Model = presetAPIModel(catalog, "tts", p.TTSProvider, p.TTSModel)
		out.tts.Voice = p.TTSVoice
		out.tts.ContextTurns = p.TTSContextTurns
		if p.TTSContextTurns > 0 {
			// Announced, because it retains audio in memory for longer than a
			// turn. Silent would be the wrong kind of convenient.
			fmt.Println(shell.Step(shell.StateIdle, "conversational context",
				fmt.Sprintf("keeping the last %d turns so the voice follows the "+
					"conversation — in memory only, dropped on /blackbox off",
					p.TTSContextTurns)))
		}
		out.tts.BaseURL = autoAssignSidecarPort("tts", p.TTSProvider, cfg.Speech.TTS.BaseURL)
		prepareSpeechProvider("tts", p.TTSProvider)
		out.tts.Fallbacks = p.TTSFallbacks
		for _, name := range out.tts.Fallbacks {
			autoAssignSidecarPort("tts", name, "")
			prepareSpeechProvider("tts", name)
		}
	}
	return out
}

// presetAPIModel resolves what to persist as the model.
//
// Local sidecar entries must persist an EMPTY model — the adapter's own default
// is correct and sending a display string like "piper (sidecar)" as a model
// name is a 400 waiting to happen. That rule already lives in
// PricingEntry.APIModel, so this defers to the catalog rather than restating it.
func presetAPIModel(catalog []speech.PricingEntry, kind, provider, model string) string {
	for _, e := range catalog {
		if e.Kind == kind && e.Provider == provider {
			if e.Local {
				return ""
			}
			if model != "" {
				return model
			}
			return e.APIModel()
		}
	}
	if isLocalSpeechProvider(provider) {
		return ""
	}
	return model
}
