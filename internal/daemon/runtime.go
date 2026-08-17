// internal/daemon/runtime.go
// Purpose: Daemon runtime — owns a headless Agent, session memory, the undo
// journal, the interaction journal, an opt-in hands-free voice loop
// (wake → transcribe → pipeline), and a connectivity monitor for graceful
// degradation. Process-level restart is the service manager's job (the 4D
// installers); in-process, every background goroutine runs under
// diagnostics.Guard so a panic leaves a crash report and the IPC loop lives.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/ambient"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/diagnostics"
	"helix/internal/input"
	"helix/internal/ollama"
	"helix/internal/session"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/wakeword"
)

// failClosedPrompter is the daemon's Prompter: with no human at a terminal,
// every confirmation declines (ADR-005 fail-closed; approvals happen at the
// terminal or through the voice confirm loop, never unsupervised).
type failClosedPrompter struct{}

func (failClosedPrompter) AskYesNo(string) bool                     { return false }
func (failClosedPrompter) AskLine(string) string                    { return "" }
func (failClosedPrompter) AskTypedConfirmation(string, string) bool { return false }

// daemonRenderer keeps the agent headless while capturing AI messages so
// IPC submits can return the reply text.
type daemonRenderer struct {
	agent.HeadlessRenderer
	mu      sync.Mutex
	last    string
	lastErr string
}

func (d *daemonRenderer) PrintAIMessage(t string, _ bool) {
	d.mu.Lock()
	d.last = truncateRunes(t, 2000)
	d.mu.Unlock()
}

// PrintError captures the last error so IPC submits can report failure
// instead of returning {"ok":true,"reply":""} — a failed plan used to be
// indistinguishable from success over the wire.
func (d *daemonRenderer) PrintError(t string) {
	d.mu.Lock()
	d.lastErr = truncateRunes(t, 2000)
	d.mu.Unlock()
}

// truncateRunes caps a string at n runes WITHOUT splitting a UTF-8 sequence
// mid-rune (a byte-slice cut could corrupt the final character).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// takeResult returns the captured reply and error, clearing both.
func (d *daemonRenderer) takeResult() (reply, errText string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reply, errText = d.last, d.lastErr
	d.last, d.lastErr = "", ""
	return
}

// Daemon is the persistent service.
type Daemon struct {
	agent    *agent.Agent
	sess     *session.RingStore
	undo     *session.UndoJournal
	journal  *Journal
	renderer *daemonRenderer
	server   *Server

	mu        sync.Mutex // serializes submits (the agent turn loop is single-threaded)
	startedAt time.Time
	stopping  chan struct{}
	stopOnce  sync.Once // guards close(stopping) against repeated/racing stop requests

	breakReminderMin int // focus-break reminder cadence; 0 = off

	llmFallback config.LLMFallbackConfig // P11.2/P11.3 offline-brain settings

	healthMu sync.Mutex
	sidecars map[string]string // component → health state ("ok" | error detail)
}

// New builds the daemon's world: config, environment, sandbox, speech
// engine, session memory, journals, headless Agent, IPC listener. Speech
// failures are journaled and tolerated — an offline daemon still serves
// text submits.
func New() (*Daemon, error) {
	cfg, err := config.DefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	// CRITICAL: the daemon runs BEFORE main() reaches its own ai.InitProviders
	// call, so without this every planner/chat request in the daemon fails
	// with "no AI provider configured" and IPC submits silently return an
	// empty reply. Initialize the LLM registry from persisted config here.
	if ierr := ai.InitProviders(ai.ProviderSettings{
		Provider:        cfg.Provider,
		Model:           cfg.ProviderModel,
		CustomBaseURL:   cfg.CustomProviderBaseURL,
		LlamaCppBaseURL: cfg.LLM.LlamaCppURL,
	}); ierr != nil {
		return nil, fmt.Errorf("ai providers: %w", ierr)
	}
	if cfg.Provider != "" {
		_ = ai.UseProvider(cfg.Provider)
		if cfg.ProviderModel != "" {
			ai.UseModel(cfg.ProviderModel)
		}
	}

	journal, err := NewJournal()
	if err != nil {
		return nil, err
	}

	// P11.2: arm the cloud→local brain failover. A misconfigured fallback is
	// journaled and tolerated — an unprotected daemon still serves requests,
	// and refusing to start over it would be a worse failure than the one it
	// guards against.
	if ferr := ai.ConfigureLocalFallback(cfg.AIFallback()); ferr != nil {
		journal.Record("lifecycle", "", "", "llm fallback disabled: "+ferr.Error())
	}
	sess, err := session.NewRingStore(cfg.Daemon.SessionTurns)
	if err != nil {
		return nil, err
	}
	undo, err := session.NewUndoJournal()
	if err != nil {
		return nil, err
	}

	if err := speech.Init(speech.Config{
		STT: speech.STTConfig{
			Provider: cfg.Speech.STT.Provider, Model: cfg.Speech.STT.Model,
			BaseURL: cfg.Speech.STT.BaseURL, Fallbacks: cfg.Speech.STT.Fallbacks,
			StreamChunkMs: cfg.Speech.STT.StreamChunkMs,
		},
		TTS: speech.TTSConfig{
			Provider: cfg.Speech.TTS.Provider, Model: cfg.Speech.TTS.Model,
			Voice: cfg.Speech.TTS.Voice, BaseURL: cfg.Speech.TTS.BaseURL,
			Fallbacks: cfg.Speech.TTS.Fallbacks, FirstByteMs: cfg.Speech.TTS.FirstByteMs,
		},
	}); err != nil {
		journal.Record("lifecycle", "", "", "speech init failed: "+err.Error())
	}

	// A background daemon has no meaningful launch cwd (LaunchAgents/systemd
	// run it from $HOME or /): root the directory sandbox at the user's home
	// and give the process a stable working directory.
	if home, herr := os.UserHomeDir(); herr == nil {
		_ = os.Chdir(home)
	}

	env := shell.DetectEnvironment()
	renderer := &daemonRenderer{}
	ag := agent.NewAgentWithRenderer(env, nil, commands.NewDirectorySandbox(),
		commands.DefaultExecuteConfig(), false, nil, nil, nil, renderer)
	ag.Session = sess
	ag.Undo = undo
	ag.OnSpeak = func(text string) {
		if !speech.TTSEnabled() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = speech.SpeakStream(ctx, text)
	}
	commands.SetPrompter(failClosedPrompter{})

	server, err := Listen()
	if err != nil {
		return nil, err
	}

	d := &Daemon{
		agent: ag, sess: sess, undo: undo, journal: journal,
		renderer: renderer, server: server,
		startedAt: time.Now(), stopping: make(chan struct{}),
		breakReminderMin: cfg.Daemon.BreakReminderMin,
		sidecars:         make(map[string]string),
		llmFallback:      cfg.LLM.Fallback,
	}

	// Brain switches are spoken AND journaled: the user hears why answers
	// suddenly got shorter, and the journal keeps the evidence for later.
	ai.SetFailoverNotice(func(msg string) {
		d.journal.Record("llm_failover", "", "", msg)
		d.speakNotice(msg)
	})

	return d, nil
}

// Addr exposes the IPC address.
func (d *Daemon) Addr() string { return d.server.Addr() }

// Handle dispatches one IPC request (Server.Handler).
func (d *Daemon) Handle(ctx context.Context, req Request) Response {
	switch req.Type {
	case TypeStatus:
		return d.statusResponse()
	case TypeSubmit:
		return d.Submit(req)
	case TypeSay:
		return d.sayRequest(ctx, req)
	case TypeMode:
		return d.modeRequest(req)
	case TypeLogTail:
		return Response{Type: TypeResponse, OK: true,
			Meta: map[string]any{"entries": d.journal.Tail(20)}}
	case TypeStop:
		// Concurrent IPC connections mean two stop requests can race; a bare
		// close() would panic ("close of closed channel"). sync.Once makes
		// stop idempotent.
		d.stopOnce.Do(func() {
			d.journal.Record("lifecycle", "", "", "stop requested via IPC")
			close(d.stopping)
		})
		return Response{Type: TypeResponse, OK: true, Meta: map[string]any{"stopping": true}}
	default:
		return Response{Type: TypeResponse, OK: false,
			Error: fmt.Sprintf("unknown request type %q", req.Type)}
	}
}

func (d *Daemon) statusResponse() Response {
	state := map[string]any{
		"started_at":  d.startedAt.Format(time.RFC3339),
		"uptime_s":    int(time.Since(d.startedAt).Seconds()),
		"addr":        d.server.Addr(),
		"tts_enabled": speech.TTSEnabled(),
		// P11.2: surface which brain is answering. "provider" is the one in
		// force right now, so a degraded daemon reports the local model rather
		// than the configured cloud one it can no longer reach.
		"provider":       ai.ActiveProviderName(),
		"model":          ai.ActiveModel(),
		"llm_fallback":   ai.FailoverStatus(),
		"llm_local_mode": ai.LocalFallbackActive(),
	}
	if reg := speech.Default(); reg != nil {
		state["stt_chain"] = strings.Join(reg.STTChain(), " → ")
		state["tts_chain"] = strings.Join(reg.TTSChain(), " → ")
	}
	if rec, err := speech.DetectRecorder(); err == nil {
		state["recorder"] = rec
	}
	// Hands-free readiness: is the wake loop actually running?
	cfg, _ := config.DefaultConfig()
	state["wake_enabled"] = cfg.Speech.WakeWord.Enabled
	state["wake_phrase"] = cfg.Speech.WakeWord.Phrase
	state["voice_loop"] = cfg.Speech.WakeWord.Enabled && speech.Default() != nil
	d.healthMu.Lock()
	if len(d.sidecars) > 0 {
		state["sidecar_health"] = d.sidecars
	}
	d.healthMu.Unlock()
	return Response{Type: TypeResponse, OK: true, State: state}
}

func (d *Daemon) modeRequest(req Request) Response {
	voice := strings.EqualFold(req.Text, "voice") || strings.EqualFold(req.Text, "on")
	return Response{Type: TypeResponse, OK: true,
		Meta: map[string]any{"mode": map[bool]string{true: "voice", false: "manual"}[voice]}}
}

// Submit runs one input event through the agent pipeline (serialized).
func (d *Daemon) Submit(req Request) Response {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return Response{Type: TypeResponse, OK: false, Error: "empty submit"}
	}

	channel := input.ChannelText
	if req.Channel == string(input.ChannelVoice) {
		channel = input.ChannelVoice
	}

	// Single-instance coordination (ADR-004, threat V7): while an interactive
	// TTY session holds a fresh active-session lock, the foreground session
	// owns the machine — the daemon refuses injected submits.
	if ttyActive() {
		return Response{Type: TypeResponse, OK: false,
			Error: "an interactive Helix session is active — remote submit is locked"}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.journal.Record("submit", string(channel), text, "")
	d.agent.HandleInputEvent(input.InputEvent{Text: text, Channel: channel, Meta: req.Meta})
	reply, errText := d.renderer.takeResult()
	if errText != "" {
		d.journal.Record("submit", string(channel), text, "error: "+errText)
		return Response{Type: TypeResponse, OK: false, Error: errText,
			Meta: map[string]any{"reply": reply}}
	}
	return Response{Type: TypeResponse, OK: true, Meta: map[string]any{"reply": reply}}
}

// sayRequest speaks text via the TTS chain (the `helix remote say` verb).
// Unlike Submit, it never touches the agent pipeline — it is pure output.
func (d *Daemon) sayRequest(ctx context.Context, req Request) Response {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return Response{Type: TypeResponse, OK: false, Error: "empty say"}
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := speech.Speak(sctx, text); err != nil {
		return Response{Type: TypeResponse, OK: false, Error: err.Error()}
	}
	d.journal.Record("say", "", text, "")
	return Response{Type: TypeResponse, OK: true}
}

// Run serves IPC and background loops until ctx cancellation or TypeStop.
func (d *Daemon) Run(ctx context.Context) error {
	d.journal.Record("lifecycle", "", "", "daemon started on "+d.server.Addr())
	defer d.journal.Record("lifecycle", "", "", "daemon stopped")

	// Ambient presence v1 (P4.13): a spoken greeting on start, and — only when
	// configured — a periodic focus-break reminder.
	d.speakNotice("Helix daemon online.")

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-d.stopping:
			cancel()
		case <-runCtx.Done():
		}
	}()

	go func() {
		defer diagnostics.Guard("daemon-connectivity")()
		d.watchConnectivity(runCtx)
	}()

	go func() {
		defer diagnostics.Guard("daemon-sidecar-health")()
		d.sidecarHealthLoop(runCtx)
	}()

	go func() {
		defer diagnostics.Guard("daemon-llm-ready")()
		d.ensureLocalBrainReady(runCtx)
	}()

	if cfg, err := config.DefaultConfig(); err == nil && cfg.Speech.WakeWord.Enabled {
		go func() {
			defer diagnostics.Guard("daemon-voice-loop")()
			d.voiceLoop(runCtx)
		}()
	}

	if d.breakReminderMin > 0 {
		go func() {
			defer diagnostics.Guard("daemon-break-reminder")()
			d.breakReminderLoop(runCtx)
		}()
	}

	return d.server.Serve(runCtx, d)
}

// watchConnectivity journals and announces online/offline transitions.
func (d *Daemon) watchConnectivity(ctx context.Context) {
	wasOnline := true
	// ≤5s detection (P4.10 acceptance): a cheap TCP dial probe on a 5s tick.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			online := checkOnline(ctx)
			if online == wasOnline {
				continue
			}
			wasOnline = online
			// Ears, voice, and brain move together (P11.2): before this, the
			// speech chain went local-first while the planner kept dialing a
			// dead cloud endpoint, so Helix could still hear and speak but
			// could no longer think. ai.SetOfflineMode fires its own spoken
			// notice only when a local brain is actually reachable, so a
			// machine with no local model stays silent about it.
			if online {
				speech.SetOfflineMode(false)
				ai.SetOfflineMode(false)
				d.journal.Record("connectivity", "", "", "online — restored configured speech + LLM chains")
				d.speakNotice("Internet connection restored.")
			} else {
				speech.SetOfflineMode(true)
				ai.SetOfflineMode(true)
				d.journal.Record("connectivity", "", "", "offline — switched speech + LLM chains to local fallback")
				d.speakNotice("I lost internet connection. Switching to local processing; some features may be limited.")
			}
		}
	}
}

// breakReminderLoop speaks a focus-break nudge every configured interval.
// v1 uses wall-clock time since daemon start as an honest proxy for focus
// time (activity tracking is a post-BlackBox idea).
func (d *Daemon) breakReminderLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.breakReminderMin) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.journal.Record("presence", "", "", "break reminder")
			d.speakNotice("You have been working for a while — consider taking a short break.")
		}
	}
}

func (d *Daemon) speakNotice(text string) {
	if d.agent.OnSpeak != nil {
		d.agent.OnSpeak(text)
	}
}

// ensureLocalBrainReady verifies at startup that the configured offline brain
// can actually answer when the cloud disappears (P11.3).
//
// A fallback that is configured but not pulled is worse than no fallback: it
// looks armed in /doctor and then fails at the one moment it was supposed to
// save the session. This runs the check once, at startup, while there is still
// a network to fix it with.
//
// Consent (guardrail §12 #1): the model PULL is a multi-gigabyte download and
// happens only when `llm.fallback.ensure_ready` is explicitly true. Otherwise
// the daemon verifies and journals — the diagnosis without the surprise.
//
// Runs in the background: a cold Ollama start plus a model pull can take
// minutes, and the IPC listener must be serving long before that finishes.
func (d *Daemon) ensureLocalBrainReady(ctx context.Context) {
	if !d.llmFallback.FallbackEnabled() {
		return
	}
	provider := d.llmFallback.Provider
	if provider == "" {
		provider = config.LLMDefaults().Fallback.Provider
	}
	// llama.cpp is a user-managed sidecar with no install/pull API (ADR-002,
	// P7.7): llama-server loads its GGUF at launch. Reachability is all Helix
	// can check, and sidecarHealthLoop already reports it.
	if provider != "ollama" {
		return
	}

	model := d.llmFallback.Model
	if model == "" {
		// The active model is the honest default: if the user already runs
		// Ollama as their main provider, that is the model to keep ready.
		model = ai.ActiveModel()
	}

	readyCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	if err := ollama.EnsureRunning(readyCtx); err != nil {
		d.journal.Record("llm_ready", "", "",
			"local fallback unavailable — Ollama is not running: "+err.Error())
		return
	}
	if model == "" {
		d.journal.Record("llm_ready", "", "",
			"Ollama reachable; no fallback model configured (llm.fallback.model)")
		return
	}

	client := ollama.NewClient()
	installed, err := client.ListModels(readyCtx)
	if err != nil {
		d.journal.Record("llm_ready", "", "", "could not list Ollama models: "+err.Error())
		return
	}
	for _, m := range installed {
		// Ollama reports tags as "name:tag"; a bare "llama3.2" must match
		// "llama3.2:latest" or the daemon would re-pull an installed model.
		if m.ID == model || strings.HasPrefix(m.ID, model+":") {
			d.journal.Record("llm_ready", "", "", "local fallback ready: "+model)
			return
		}
	}

	if !d.llmFallback.EnsureReady {
		d.journal.Record("llm_ready", "", "", fmt.Sprintf(
			"local fallback model %q is NOT pulled — offline mode will have no brain. "+
				"Run `ollama pull %s`, or set llm.fallback.ensure_ready=true to let the daemon pull it.",
			model, model))
		return
	}

	d.journal.Record("llm_ready", "", "", "pulling local fallback model "+model)
	if err := client.PullModel(readyCtx, model, nil); err != nil {
		d.journal.Record("llm_ready", "", "", "pull failed for "+model+": "+err.Error())
		return
	}
	d.journal.Record("llm_ready", "", "", "local fallback ready: "+model)
}

// sidecarHealthLoop polls local sidecars (Ollama, whisper, piper, wake word)
// and refreshes the status surface (P4.7).
func (d *Daemon) sidecarHealthLoop(ctx context.Context) {
	d.refreshSidecars(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.refreshSidecars(ctx)
		}
	}
}

// refreshSidecars probes each local component under one bounded timeout.
func (d *Daemon) refreshSidecars(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	results := map[string]string{}
	probe := func(name string, fn func(context.Context) error) {
		if err := fn(probeCtx); err != nil {
			results[name] = err.Error()
		} else {
			results[name] = "ok"
		}
	}

	probe("ollama", func(ctx context.Context) error { return ollama.NewClient().Health(ctx) })

	if reg := speech.Default(); reg != nil {
		for _, name := range reg.STTNames() {
			if p, ok := reg.STTProvider(name); ok && p.IsLocal() {
				p := p
				probe("stt."+name, p.HealthCheck)
			}
		}
		for _, name := range reg.TTSNames() {
			if p, ok := reg.TTSProvider(name); ok && p.IsLocal() {
				p := p
				probe("tts."+name, p.HealthCheck)
			}
		}
	}

	if cfg, err := config.DefaultConfig(); err == nil && cfg.Speech.WakeWord.Engine == "sidecar" &&
		cfg.Speech.WakeWord.SidecarURL != "" {
		det := wakeword.NewSidecarDetector(cfg.Speech.WakeWord.SidecarURL,
			cfg.Speech.WakeWord.Phrase, wakeword.Preset(cfg.Speech.WakeWord.SensitivityPreset))
		probe("wakeword", det.Health)
	}

	d.healthMu.Lock()
	d.sidecars = results
	d.healthMu.Unlock()
}

// checkOnline performs a bounded TCP probe against a stable endpoint.
func checkOnline(ctx context.Context) bool {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", "api.github.com:443")
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// maxVoiceTries is how many capture attempts follow a wake before the daemon
// returns to wake-only listening. Kept small: a silent room shouldn't loop
// the mic forever, but a single miss shouldn't drop the user's request.
const maxVoiceTries = 3

// voiceLoop is the daemon's hands-free presence: wake-only listening, then
// a full voice turn. Pauses while an interactive TTY session holds the
// active-session lock (mic ownership coordination, threat V7).
func (d *Daemon) voiceLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if ttyActive() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if !d.awaitWake(ctx) {
			return
		}
		// After a wake, allow a few capture attempts with spoken feedback so
		// a silent/mumbled first pass doesn't silently drop the request.
		for attempt := 0; attempt < maxVoiceTries; attempt++ {
			if d.runVoiceTurn(ctx) {
				break // a valid turn was submitted
			}
			if attempt+1 < maxVoiceTries {
				d.speakNotice("I didn't catch that. Please repeat.")
			}
		}
	}
}

// awaitWake blocks until a wake event; false = ctx ended or voice hardware
// absent (retry after a pause so a later `helix daemon` restart or hotplug
// still works).
func (d *Daemon) awaitWake(ctx context.Context) bool {
	pause := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(60 * time.Second):
			return true
		}
	}

	if speech.Default() == nil {
		return pause()
	}
	if _, err := speech.DetectRecorder(); err != nil {
		return pause()
	}

	svc, err := d.buildWakeService()
	if err != nil {
		return pause()
	}
	events, err := svc.Start(ctx)
	if err != nil {
		return pause()
	}
	defer func() { _ = svc.Stop() }()

	select {
	case ev := <-events:
		d.journal.Record("wake", "", ev.Phrase, fmt.Sprintf("score %.2f", ev.Score))
		return true
	case <-ctx.Done():
		return false
	}
}

// buildWakeService assembles the wake loop from config (energy default,
// sidecar for real keyword spotting — mirrors voice_mode.go). When ambient
// awareness is enabled, the chunk stream is teed into the ambient monitor
// (Phase 6 shares the capture stream, roadmap P6.1).
func (d *Daemon) buildWakeService() (wakeword.Service, error) {
	cfg, err := config.DefaultConfig()
	if err != nil {
		return nil, err
	}
	preset := wakeword.Preset(cfg.Speech.WakeWord.SensitivityPreset)
	var detector wakeword.Detector
	if cfg.Speech.WakeWord.Engine == "sidecar" {
		detector = wakeword.NewSidecarDetector(cfg.Speech.WakeWord.SidecarURL,
			cfg.Speech.WakeWord.Phrase, preset)
	} else {
		detector = wakeword.NewEnergyDetector(preset)
	}
	chunkMs := cfg.Speech.WakeWord.ChunkMs
	if chunkMs <= 0 {
		chunkMs = 1500
	}

	scanner := wakeword.Scanner(wakeword.NewSoXScanner(time.Duration(chunkMs)*time.Millisecond, 16000))
	if cfg.Ambient.Enabled {
		scanner = ambient.Tee(scanner, d.ambientMonitor(cfg))
	}

	return wakeword.NewService(
		scanner,
		detector,
		wakeword.Config{
			Phrase:   cfg.Speech.WakeWord.Phrase,
			Cooldown: time.Duration(cfg.Speech.WakeWord.CooldownS) * time.Second,
			OnError:  func(error) {},
		})
}

// ambientMonitor builds the monitor for the daemon's wake stream.
func (d *Daemon) ambientMonitor(cfg *config.Config) *ambient.ChunkMonitor {
	enabled := map[ambient.Category]bool{}
	for name, on := range cfg.Ambient.Categories {
		enabled[ambient.Category(name)] = on
	}
	svc := ambient.NewServiceFromOptions(cfg.Ambient.Sensitivity,
		ambient.ResponseModeFromString(cfg.Ambient.ResponseMode), enabled)
	mon := ambient.NewChunkMonitor(svc)
	mon.OnSpeak = d.speakNotice
	mon.OnLog = func(ev ambient.Event) {
		d.journal.Record("ambient", "", string(ev.Category), fmt.Sprintf("intensity %.2f", ev.Intensity))
	}
	return mon
}

// runVoiceTurn performs one hands-free voice exchange through the pipeline.
// Returns true when a non-empty transcript was submitted (the exchange
// succeeded); false on silence, empty transcription, or a capture/STT error so
// the loop can offer a spoken retry.
func (d *Daemon) runVoiceTurn(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()

	clip, err := speech.RecordClip(cctx, speech.CaptureOptions{MaxDuration: 12 * time.Second})
	if err != nil {
		d.journal.Record("error", "voice", "", "capture: "+err.Error())
		return false
	}

	// Amplitude AND duration gate BEFORE the STT round-trip (mirrors the
	// interactive loop): a dead mic, a silent room, or a sub-0.3s transient must
	// not burn a cloud transcription. The daemon speaks its own prompts, so the
	// duration half matters here too — the tail of a spoken notice is exactly
	// the kind of clip STT hallucinates a word out of.
	if !speech.UsableSpeech(clip) {
		d.journal.Record("voice", "", "", "no speech detected — re-arming")
		return false
	}

	transcript, err := speech.Transcribe(cctx, clip)
	if err != nil {
		d.journal.Record("error", "voice", "", "transcribe: "+err.Error())
		return false
	}
	text := strings.TrimSpace(transcript.Text)
	if text == "" {
		d.journal.Record("voice", "", "", "empty transcript — re-arming")
		return false
	}

	d.journal.Record("submit", "voice", text, "hands-free")
	d.mu.Lock()
	d.agent.HandleInputEvent(input.InputEvent{
		Text: text, Channel: input.ChannelVoice,
		Meta: map[string]any{
			"stt_provider":   transcript.Provider,
			"stt_confidence": transcript.Confidence,
		},
	})
	d.mu.Unlock()
	return true
}

// ttyActive reports whether an interactive Helix session holds the
// active-session lock (fresh heartbeat = within 5 minutes).
func ttyActive() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(home + "/.helix/active.lock")
	if err != nil {
		return false
	}
	var lock struct {
		Kind string `json:"kind"`
		TS   int64  `json:"ts"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return false
	}
	return lock.Kind == "tty" && time.Since(time.Unix(lock.TS, 0)) < 5*time.Minute
}

// Heartbeat writes the TTY active-session lock (called by the interactive
// REPL each turn; the daemon pauses its voice loop while the lock is fresh,
// yielding the microphone to the foreground session).
func Heartbeat() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := home + "/.helix"
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(dir+"/active.lock",
		[]byte(fmt.Sprintf(`{"kind":"tty","pid":%d,"ts":%d}`, os.Getpid(), time.Now().Unix())), 0o600)
}
