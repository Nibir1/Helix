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

	STTProvider  string
	STTModel     string
	STTFallbacks []string

	TTSProvider  string
	TTSModel     string
	TTSVoice     string
	TTSFallbacks []string
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
		it := shell.MenuItem{Label: p.Name, Note: p.Note}
		if i == 0 {
			it.Tag, it.Good = "recommended", true
		}
		if p.needsKey() {
			it.Tag = "needs a key"
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
