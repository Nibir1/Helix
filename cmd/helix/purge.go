// cmd/helix/purge.go
//
// Purpose: /purge — full local data wipe for a clean-slate restart.
// Deletes provider API keys, the knowledge database (plus SQLite journals),
// all vector/RAG/MAN caches, preferences, history, logs, and temp artifacts
// after an explicit warning and y/N confirmation. Model weights are removed
// only with a second explicit confirmation.
//
// The manifest is GROUPED, which is a safety property rather than decoration.
// It used to be one undifferentiated yellow wall in which
// "provider API keys (all providers)" carried exactly the weight of
// "SQLite shared-memory journal" — so the one genuinely irreplaceable line on
// the screen was the hardest to notice, in a list whose entire job is to be
// read before an irreversible yes. Credentials now stand alone and first, the
// three SQLite journals and the log sweep collapse into sections the eye can
// skip, and the count per group says how much is really at stake.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// purgeGroup orders and titles a section of the manifest.
//
// Order is by irreplaceability, not by directory layout: a key you cannot get
// back without a trip to a vendor dashboard comes before a cache that rebuilds
// itself on the next boot.
type purgeGroup int

const (
	groupCredentials purgeGroup = iota
	groupKnowledge
	groupSettings
	groupMemory
	groupRuntime
)

func (g purgeGroup) title() string {
	switch g {
	case groupCredentials:
		return "credentials"
	case groupKnowledge:
		return "knowledge"
	case groupSettings:
		return "settings"
	case groupMemory:
		return "memory and history"
	default:
		return "caches, locks and logs"
	}
}

// note is the one-line consequence of losing this whole group, said once
// instead of implied by every row.
func (g purgeGroup) note() string {
	switch g {
	case groupCredentials:
		return "you will have to paste every key again"
	case groupKnowledge:
		return "rebuilt by /update — a download, not a loss"
	case groupSettings:
		return "back to first-run defaults"
	case groupMemory:
		return "conversations, transcripts and undo history — not recoverable"
	default:
		return "recreated automatically on the next boot"
	}
}

// purgeTarget describes one filesystem path removed by /purge.
type purgeTarget struct {
	path  string
	desc  string
	group purgeGroup
}

// handlePurgeCommand implements the /purge flow:
// manifest panel → y/N confirmation → optional model wipe → deletion report.
//
// Args: none.
// Returns: none.
// Complexity: O(n) in the number of purge targets.
func handlePurgeCommand() {
	home, err := os.UserHomeDir()
	if err != nil {
		color.Red("Purge failed: cannot resolve home directory: %v", err)
		return
	}
	helixDir := filepath.Join(home, ".helix")

	targets := purgeTargets(home, helixDir)
	present := make([]purgeTarget, 0, len(targets))
	for _, t := range targets {
		if pathExists(t.path) {
			present = append(present, t)
		}
	}

	if len(present) == 0 {
		fmt.Println(shell.PanelTitle("purge"))
		fmt.Println(shell.Step(shell.StateGood, "nothing to purge",
			"Helix is already a blank slate here"))
		fmt.Println(shell.PanelEnd())
		return
	}

	weights := existingWeights(purgeWeightTargets(helixDir))
	printPurgeManifest(home, helixDir, present, weights)

	if !wizConfirmDanger(fmt.Sprintf("permanently delete these %d item(s)", len(present))) {
		fmt.Println(shell.Step(shell.StateIdle, "cancelled", "nothing was deleted"))
		return
	}

	// Second confirmation for the downloads, which are large, slow to replace,
	// and nobody's data. Only asked when at least one of them EXISTS: a prompt
	// whose yes deletes nothing is worse than no prompt, and that is exactly
	// what this one was — see purgeWeightTargets.
	if len(weights) > 0 {
		var total int64
		for _, wt := range weights {
			total += wt.size
		}
		if wizConfirmDanger(fmt.Sprintf(
			"also delete the %d download(s) above, freeing %s",
			len(weights), compactBytes(total))) {
			for _, wt := range weights {
				present = append(present, wt.purgeTarget)
			}
		}
	}

	deleted, failures := runPurge(present)
	printPurgeResult(deleted, failures)
}

// weightTarget is a downloaded artifact, carried with its size.
//
// Size only matters here. The main manifest is about what you LOSE, and a byte
// count says nothing useful about losing your API keys; this section is about
// disk space, and the number is the entire reason anyone answers yes.
type weightTarget struct {
	purgeTarget
	size int64
}

// purgeWeightTargets is every large download Helix makes under ~/.helix.
//
// This list is why /purge's second prompt existed and did nothing. It offered
// "~/.helix/models" alone — the llama.cpp GGUF directory — while the artifacts
// Helix actually downloads through its own wizard land in `whisper-models`
// (a few hundred MB of GGML), `piper-voices` (the ONNX voice) and `piper` (the
// runtime binary the setup installs). A user who answered YES on a machine set
// up entirely through /blackbox setup deleted nothing at all, and was told the
// weights had been kept by a manifest line that was right by accident.
//
// HELIX_MODEL_DIR is honoured because config.DefaultConfig honours it: purging
// the default path on a machine that keeps its GGUFs elsewhere would silently
// miss the largest directory on the disk.
func purgeWeightTargets(helixDir string) []purgeTarget {
	modelDir := strings.TrimSpace(os.Getenv("HELIX_MODEL_DIR"))
	if modelDir == "" {
		modelDir = filepath.Join(helixDir, "models")
	}
	return []purgeTarget{
		{modelDir, "LLM model weights (GGUF)", groupRuntime},
		{filepath.Join(helixDir, "whisper-models"), "speech-to-text models", groupRuntime},
		{filepath.Join(helixDir, "piper-voices"), "text-to-speech voices", groupRuntime},
		{filepath.Join(helixDir, "piper"), "the piper runtime binary", groupRuntime},
	}
}

// existingWeights keeps only what is on disk, measured.
func existingWeights(targets []purgeTarget) []weightTarget {
	out := make([]weightTarget, 0, len(targets))
	for _, t := range targets {
		if !pathExists(t.path) {
			continue
		}
		out = append(out, weightTarget{purgeTarget: t, size: dirSize(t.path)})
	}
	return out
}

// dirSize sums a tree, tolerating unreadable entries.
//
// Best-effort on purpose: a size is here to inform a decision, and refusing to
// print one because a single file could not be stat'd would remove the only
// number on the screen worth reading.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// purgeTargets is the full manifest, grouped.
//
// Every path here is recreated automatically on the next boot EXCEPT the
// credentials and the memory group, which is exactly why those two are named
// separately on screen.
func purgeTargets(home, helixDir string) []purgeTarget {
	in := func(name string) string { return filepath.Join(helixDir, name) }

	targets := []purgeTarget{
		{in("secrets.json"), "provider API keys (all providers, incl. STT/TTS)", groupCredentials},
		{in("openai_api_key"), "legacy OpenAI key file", groupCredentials},

		{in("helix.db"), "knowledge database (CVE / KEV / Exploit-DB / MITRE + FTS)", groupKnowledge},
		{in("helix.db-wal"), "SQLite WAL journal", groupKnowledge},
		{in("helix.db-shm"), "SQLite shared-memory journal", groupKnowledge},
		{in("rag_index"), "RAG state and index cache", groupKnowledge},
		{in("vector_index"), "vector embedding index", groupKnowledge},
		{in("man_index"), "MAN page index cache", groupKnowledge},

		{in("config.json"), "user preferences and provider settings", groupSettings},
		{in("pricing.json"), "user pricing overrides", groupSettings},
		{in("hooks.json"), "local policy hooks (/hooks)", groupSettings},
		{in("todo.json"), "task list (/todo)", groupSettings},

		// BlackBox artifacts (threat V5: voice/vision data is wipeable).
		{in("session.json"), "voice/session conversation memory", groupMemory},
		{in("reboot.json"), "what /reboot was carrying across a restart", groupMemory},
		{in("journal"), "interaction + undo journals", groupMemory},
		{in("voice_log"), "opt-in voice transcripts", groupMemory},
		{in("trash"), "undo staging", groupMemory},
		{filepath.Join(home, ".helix_history"), "command history", groupMemory},
		{in(".helix_history"), "legacy command history", groupMemory},
		// Harness state. A purge that left these behind would hand the "clean
		// slate" a task list, an archive of past conversations, and hook
		// commands that still fire.
		{in("sessions"), "archived conversations (/resume)", groupMemory},
		{in("exports"), "exported transcripts (/export)", groupMemory},

		{in("metrics"), "wake/latency metrics", groupRuntime},
		{in("daemon.sock"), "daemon IPC socket", groupRuntime},
		{in("daemon.conn.json"), "daemon connection info", groupRuntime},
		{in("active.lock"), "active-session lock", groupRuntime},
		{in("update-pending"), "a note for the restart supervisor", groupRuntime},
	}

	// Sweep log, temp, and crash-report artifacts from the Helix home.
	for _, pattern := range []string{"*.log", "*.tmp", "crash-*.json"} {
		matches, _ := filepath.Glob(filepath.Join(helixDir, pattern))
		for _, m := range matches {
			targets = append(targets, purgeTarget{m, "log/temp artifact", groupRuntime})
		}
	}
	return targets
}

// printPurgeManifest renders exactly what exists and will be destroyed.
func printPurgeManifest(home, helixDir string, present []purgeTarget, weights []weightTarget) {
	fmt.Println(shell.PanelTitle("purge"))
	fmt.Println(shell.Step(shell.StateBad, "permanent",
		fmt.Sprintf("%d item(s) will be deleted and cannot be recovered", len(present))))
	fmt.Println(shell.KV("IN", shell.Value(helixDir)+shell.Muted("  and $HOME"),
		shell.KVWidth("IN")))

	for _, g := range []purgeGroup{
		groupCredentials, groupKnowledge, groupSettings, groupMemory, groupRuntime,
	} {
		rows := make([]purgeTarget, 0, len(present))
		for _, t := range present {
			if t.group == g {
				rows = append(rows, t)
			}
		}
		if len(rows) == 0 {
			continue
		}

		fmt.Println(shell.PanelGap())
		fmt.Println(shell.PanelSection(fmt.Sprintf("%s  (%d)", g.title(), len(rows))))
		for _, l := range shell.PanelWrap(g.note(), shell.Muted) {
			fmt.Println(l)
		}

		labels := make([]string, 0, len(rows))
		for _, t := range rows {
			labels = append(labels, shortPurgePath(home, t.path))
		}
		// The PATH is the value worth reading here — it is the thing being
		// deleted — so it carries the value colour while the description stays
		// muted. KV's label column is muted by default, which would have given
		// the path and its description the same weight: the same flattening the
		// grouping above exists to undo, one column further in.
		w := shell.KVWidth(labels...)
		for i, t := range rows {
			fmt.Println(shell.KV(shell.Value(labels[i]), shell.Muted(t.desc), w))
		}
	}

	fmt.Println(shell.PanelGap())
	if len(weights) == 0 {
		// Scope, stated as a fact rather than as a promise. It used to read
		// "model weights ... are NOT deleted by default" and was contradicted by
		// the very next prompt, which offers to delete them.
		fmt.Println(shell.Step(shell.StateIdle, "no downloads found",
			"nothing large to reclaim under ~/.helix"))
		fmt.Println(shell.PanelEnd())
		return
	}

	var total int64
	labels := make([]string, 0, len(weights))
	for _, wt := range weights {
		total += wt.size
		labels = append(labels, shortPurgePath(home, wt.path))
	}
	fmt.Println(shell.PanelSection(fmt.Sprintf("downloads  (%d)", len(weights))))
	for _, l := range shell.PanelWrap(
		"KEPT unless you say otherwise at the second prompt  ·  "+
			compactBytes(total)+" in total", shell.Muted) {
		fmt.Println(l)
	}
	w := shell.KVWidth(labels...)
	for i, wt := range weights {
		fmt.Println(shell.KV(shell.Value(labels[i]),
			shell.Muted(wt.desc+"  ·  "+compactBytes(wt.size)), w))
	}
	fmt.Println(shell.PanelEnd())
}

// runPurge deletes what was confirmed, collecting failures rather than
// interleaving them with the progress.
//
// Collected on purpose: a red error printed mid-loop lands between two silent
// successes with nothing to say which is which, and the count at the end then
// contradicts the wall above it.
func runPurge(targets []purgeTarget) (deleted int, failures []string) {
	for _, t := range targets {
		if !pathExists(t.path) {
			continue
		}
		if err := os.RemoveAll(t.path); err != nil {
			failures = append(failures, t.path+": "+err.Error())
			continue
		}
		deleted++
	}
	return deleted, failures
}

// printPurgeResult closes with what actually happened.
func printPurgeResult(deleted int, failures []string) {
	fmt.Println(shell.PanelTitle("purge"))
	if len(failures) == 0 {
		fmt.Println(shell.Step(shell.StateGood, "blank slate",
			fmt.Sprintf("%d item(s) deleted", deleted)))
	} else {
		fmt.Println(shell.Step(shell.StateBad,
			fmt.Sprintf("%d failed", len(failures)),
			fmt.Sprintf("%d item(s) deleted — the rest are still on disk", deleted)))
		for _, f := range failures {
			for _, l := range shell.StepDetail(f, shell.Muted) {
				fmt.Println(l)
			}
		}
	}
	fmt.Println(shell.PanelEnd())
	// Not advice — a correctness note. SQLite handles this process still holds
	// keep deleted files alive until it exits, so "blank slate" is only true
	// after a restart. /reboot exists to make that one word rather than a quit
	// and a relaunch; it is named here because this is the screen that needs it.
	fmt.Println(shell.Hint("/reboot finishes the job — open DB handles only close on exit"))
}

// shortPurgePath renders a path relative to $HOME.
//
// The manifest repeated the same 21-character home prefix on every row, which
// pushed each description right and made the part that DIFFERS — the filename —
// the hardest thing on the line to compare. "~/" is unambiguous and the panel
// names the absolute directory once, above.
func shortPurgePath(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

// pathExists reports whether a file or directory exists on disk.
//
// Args:
//   - path: filesystem path.
//
// Returns: bool.
// Complexity: O(1).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
