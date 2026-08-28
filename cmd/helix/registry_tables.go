// cmd/helix/registry_tables.go
// Purpose: the command tables themselves, one function per section of /help.
//
// Kept apart from registry.go so the dispatch machinery stays readable while
// the tables grow. Every entry here is the single source of truth for that
// command's name, aliases, usage line, and help text.
package main

import (
	"helix/internal/agent"
	"helix/internal/hooks"
	"strings"
)

func coreCommands() []command {
	return []command{
		{
			Name: "/help", VoiceOK: true, Aliases: []string{"/?", "/h", "/sos"},
			Usage: "/help [command]", Category: catCore,
			Summary: "Show this menu, or full detail for one command",
			Detail: []string{
				"With no argument, prints every command grouped by category.",
				"With a command name (with or without the leading slash) prints that",
				"command's arguments, aliases, and what it will and will not do.",
			},
			Handler: handleHelp,
		},
		{
			Name: "/about", VoiceOK: true, Category: catCore,
			Summary: "Helix philosophy, banner & creator",
			Handler: func(cmdArgs) { handleAbout() },
		},
		{
			Name: "/version", VoiceOK: true, Aliases: []string{"/v"}, Category: catCore,
			Summary: "Version, build flavor, and platform",
			Detail: []string{
				"Reports the Helix version alongside the two build facts that change",
				"behavior invisibly: whether this binary was built with audio support",
				"(a CGO-free build cannot speak, however TTS is configured) and which",
				"kernel confinement backend is actually available.",
			},
			Handler: handleVersionCommand,
		},
		{
			Name: "/setup", Category: catCore,
			Summary: "Setup wizard: identity, AI provider, system packages, voice",
			Handler: func(cmdArgs) { handleSetup() },
		},
		{
			Name: "/config", Usage: "/config [key [value]]", Category: catCore,
			Summary: "Show or change persisted settings",
			Detail: []string{
				"With no argument, prints every settable key with its current value and",
				"where it is stored. With a key, prints just that one. With a key and a",
				"value, sets and persists it.",
				"",
				"Only curated keys are settable here. Secrets are deliberately absent:",
				"API keys go through /setup and the key store, never through a command",
				"whose arguments land in shell history.",
			},
			Handler: handleConfigCommand,
		},
		{
			Name: "/cd", VoiceOK: true, Usage: "/cd [dir]", Category: catCore,
			Summary: "Change directory (sandbox-aware)",
			Detail: []string{
				"With no argument, prints the current directory. The move is validated",
				"against the active sandbox mode, so /sandbox strict still confines it.",
			},
			Handler: handleChangeDirectory,
		},
		{
			Name: "/status", VoiceOK: true, Category: catCore,
			Summary: "Session state: RAG, provider, harness, sandbox, hooks",
			Handler: func(cmdArgs) { handleStatus() },
		},
		{
			Name: "/doctor", VoiceOK: true, Category: catCore,
			Summary: "Full system diagnostics, including edge and sidecar checks",
			Detail: []string{
				"Probes what fails silently: a mute build, degraded kernel confinement,",
				"an unreachable local model, a missing recorder, thermal throttling, and",
				"any pending crash reports. Every finding comes with its fix.",
			},
			Handler: func(cmdArgs) { handleDoctor() },
		},
		{
			Name: "/online", VoiceOK: true, Category: catCore,
			Summary: "Check internet connectivity",
			Handler: func(cmdArgs) { checkOnlineStatus() },
		},
		{
			Name: "/debug", VoiceOK: true, Usage: "/debug <on|off>", Category: catCore,
			Summary: "Toggle verbose debug logging",
			Handler: handleDebugCommand,
		},
	}
}

func sessionCommands() []command {
	return []command{
		{
			Name: "/context", VoiceOK: true, Category: catSession,
			Summary: "What the model is being told, and how big it is",
			Detail: []string{
				"Breaks down every block that goes into a planner prompt: conversation",
				"memory, open tasks, retrieved knowledge, project context, and the",
				"prompt scaffolding itself.",
				"",
				"Token figures are ESTIMATES (about 4 characters per token). No provider",
				"in the registry returns a usage block on the streaming path Helix uses,",
				"so an exact count is not available to report.",
			},
			Handler: func(cmdArgs) { handleContextCommand() },
		},
		{
			Name: "/cost", VoiceOK: true, Aliases: []string{"/usage"}, Category: catSession,
			Summary: "Model traffic for this session, by purpose",
			Detail: []string{
				"Call counts, failures, latency, and estimated tokens split by purpose",
				"(planner, chat, tool calling, vision) and by provider and model.",
				"",
				"Calls, failures, characters, and latency are exact. Token counts are",
				"estimated from length, so treat them as proportions rather than a bill.",
				"Helix ships no price table: rates change without notice, and a stale",
				"hardcoded number is worse than an honest token count.",
			},
			Handler: func(cmdArgs) { handleCostCommand() },
		},
		{
			Name: "/memory", VoiceOK: true, Usage: "/memory [show|clear]", Category: catSession,
			Summary: "Show or wipe conversation memory",
			Detail: []string{
				"Conversation memory is a bounded ring of recent turns, injected into",
				"planner prompts as a zero-authority data block — it can help the model",
				"resolve \"what did I just ask?\" and can never instruct it.",
				"",
				"clear archives the conversation first, so /resume can bring it back.",
			},
			Handler: handleMemoryCommand,
		},
		{
			Name: "/clear", VoiceOK: true, Aliases: []string{"/reset"}, Category: catSession,
			Summary: "Archive and clear the conversation, then clear the screen",
			Detail: []string{
				"Starts a fresh conversation: memory is archived to ~/.helix/sessions,",
				"then wiped, the usage meter resets, and the screen clears.",
				"",
				"Nothing is destroyed — /resume lists the archive. Tasks (/todo), the",
				"undo journal, and shell history are untouched.",
			},
			Handler: handleClearCommand,
		},
		{
			Name: "/compact", VoiceOK: true, Usage: "/compact [focus]", Category: catSession,
			Summary: "Summarize the conversation into one dense turn",
			Detail: []string{
				"Replaces the conversation with a model-written summary, keeping the",
				"thread of a long session while freeing prompt space. An optional focus",
				"argument tells the summarizer what to preserve in detail.",
				"",
				"The pre-compaction conversation is archived first, so /resume can",
				"recover it if the summary lost something that mattered.",
			},
			Handler: handleCompactCommand,
		},
		{
			Name: "/resume", VoiceOK: true, Usage: "/resume [id]", Category: catSession,
			Summary: "List archived conversations, or reload one",
			Detail: []string{
				"With no argument, lists archived conversations newest first. With an ID",
				"from that list, replaces the live conversation with the archive.",
				"",
				"The current conversation is archived before being replaced, so",
				"switching is never a one-way door.",
			},
			Handler: handleResumeCommand,
		},
		{
			Name: "/export", VoiceOK: true, Usage: "/export [path]", Category: catSession,
			Summary: "Write the conversation to a Markdown transcript",
			Detail: []string{
				"Defaults to ~/.helix/exports/. Give a path to choose the file.",
				"The transcript is plain Markdown: timestamps, channel (typed or",
				"spoken), what you asked, and what Helix replied.",
			},
			Handler: handleExportCommand,
		},
		{
			Name: "/history", VoiceOK: true, Usage: "/history [pattern]", Category: catSession,
			Summary: "Search this machine's Helix command history",
			Detail: []string{
				"With no pattern, shows the most recent lines. With a pattern, shows",
				"matching lines (case-insensitive substring).",
				"",
				"Lines entered while /stealth was on are absent by design — stealth",
				"mode suppresses the on-disk history file.",
			},
			Handler: handleHistoryCommand,
		},
	}
}

func harnessCommands() []command {
	return []command{
		{
			Name: "/agentic", VoiceOK: true, Usage: "/agentic [on|off|steps <n>]", Category: catHarness,
			Summary: "Iterative harness: observe step results and self-correct",
			Detail: []string{
				"Off by default, Helix plans once and executes. On, it observes what",
				"each step actually printed and exited with, then replans to correct",
				"failures, up to the step budget.",
				"",
				"Every follow-up plan re-enters the FULL safety pipeline: classify,",
				"instruction firewall, risk tiers, sandbox, hooks. The loop is bounded",
				"and can never bypass a gate.",
				"",
				"steps <n> retunes the budget (1-20) for this session and persists it.",
			},
			Handler: handleAgenticCommand,
		},
		{
			Name: "/plan", VoiceOK: true, Usage: "/plan <request>", Category: catHarness,
			Summary: "Show the plan for a request without executing anything",
			Detail: []string{
				"Runs the planner and the safety rewrite, then stops and prints the",
				"steps exactly as they would run. Nothing executes — this is the",
				"read-only way to inspect what a request would do.",
				"",
				"For a whole session in this posture, use /permissions plan instead.",
			},
			Handler: handlePlanCommand,
		},
		{
			Name: "/permissions", VoiceOK: true, VoiceReadOnly: true, Aliases: []string{"/mode"},
			Usage: "/permissions [mode]", Category: catHarness,
			Summary: "Approval posture: plan, cautious, ask, or auto",
			Detail: []string{
				"plan      nothing executes; steps are printed as a plan",
				"cautious  every command asks first, including low risk",
				"ask       the default: low runs, medium asks, high is blocked",
				"auto      medium risk is auto-approved",
				"",
				"The mode layers on top of the risk tiers and never replaces them.",
				"High-risk commands stay blocked in every mode, typed confirmations",
				"stay typed, the sandbox still validates, and the Voice Risk Policy",
				"still caps anything arriving by voice.",
				"",
				"Voice can ASK what the mode is; it cannot change it. Loosening the",
				"posture must be a typed decision.",
			},
			Handler: handlePermissionsCommand,
		},
		{
			Name: "/todo", VoiceOK: true, Usage: "/todo [add|start|done|rm|...]",
			Category: catHarness,
			Summary:  "Task list the planner can see",
			Detail: []string{
				"/todo                     list the tasks",
				"/todo add <text>          add one",
				"/todo start|done|block|open <id> [note]   move it",
				"/todo rm <id>             delete one",
				"/todo prune               drop completed tasks, keep the IDs shown",
				"/todo clear               empty the list entirely",
				"",
				"The visible plan of record for multi-step work, persisted across",
				"restarts. Open tasks ride into planner prompts as a zero-authority",
				"data block, so the harness can pick up where the last turn stopped.",
				"",
				"With no argument, lists the tasks. prune drops completed tasks and",
				"keeps the IDs you just read; clear empties the list entirely.",
			},
			Handler: handleTodoCommand,
		},
		{
			Name: "/tools", VoiceOK: true, Category: catHarness,
			Summary: "The harness tool vocabulary and each tool's gate",
			Detail: []string{
				"The planner's tool vocabulary is CLOSED: an unknown tool name is",
				"dropped, never dispatched. This lists what exists, whether it is",
				"usable right now, and which gate stands in front of it.",
			},
			Handler: func(cmdArgs) { handleToolsCommand() },
		},
		{
			Name: "/hooks", Usage: "/hooks [list|add|rm|test|...]",
			Category: catHarness,
			Summary:  "Run your own commands around tool execution",
			Detail: []string{
				"/hooks                    list the configured hooks",
				"/hooks events             the events a hook can fire on",
				"/hooks add                add one interactively",
				"/hooks rm <name>          delete one",
				"/hooks on|off <name>      enable or disable one",
				"/hooks test <event> <cmd> run a command once with the hook variables set",
				"",
				"A hook enforces policy Helix cannot know about: \"never touch the prod",
				"kubeconfig\", \"gofmt after any write\", \"log every push\". Hooks live in",
				"~/.helix/hooks.json and come from nowhere else — nothing a model",
				"produces can define or edit one.",
				"",
				"A blocking pre-hook that exits non-zero DENIES the step. That is the",
				"only way a hook changes control flow: hooks run after every built-in",
				"gate, so they can subtract permission and never grant it.",
				"",
				"Hook commands receive the details as environment variables",
				"(HELIX_COMMAND, HELIX_TOOL, HELIX_CWD, HELIX_EXIT_CODE, ...). Nothing",
				"is interpolated into the command string.",
			},
			Handler: handleHooksCommand,
		},
		{
			Name: "/undo", VoiceOK: true, Category: catHarness,
			Summary: "Reverse the most recent journalled action",
			Detail: []string{
				"Offers the latest entry from the undo journal with its reversal",
				"command, then runs that reversal through the full safety pipeline — an",
				"undo is never an execution bypass.",
				"",
				"The journal records actions with a known, safe reversal (currently a",
				"git commit, reversed by a soft reset). An entry is consumed only after",
				"the reversal succeeds, so it cannot be replayed twice.",
			},
			Handler: func(cmdArgs) { handleUndoCommand() },
		},
		{
			Name: "/dry-run", VoiceOK: true, Category: catHarness,
			Summary: "Toggle command execution preview mode",
			Detail: []string{
				"Prints what each step would run instead of running it. Unlike",
				"/permissions plan, this stays inside the normal turn: the planner still",
				"plans and the pipeline still validates; only the final exec is skipped.",
			},
			Handler: func(cmdArgs) { toggleDryRun() },
		},
	}
}

func devCommands() []command {
	return []command{
		{
			Name: "/init", Usage: "/init [--force]", Category: catDev,
			Summary: "Study this repository and write HELIX.md project context",
			Detail: []string{
				"Surveys the repository — languages, build and test commands, layout,",
				"conventions — and writes HELIX.md, which Helix then loads as project",
				"context on every turn in this directory.",
				"",
				"An existing HELIX.md is never overwritten without --force, and you",
				"always see the generated file before it is written.",
			},
			Handler: handleInitCommand,
		},
		{
			Name: "/diff", VoiceOK: true, Usage: "/diff [--staged] [path...]", Category: catDev,
			Summary: "Show the working tree diff with a summary",
			Handler: handleDiffCommand,
		},
		{
			Name: "/review", VoiceOK: true, Usage: "/review [--staged] [path...]", Category: catDev,
			Summary: "AI review of the current diff",
			Detail: []string{
				"Reviews what actually changed — correctness, error handling, security,",
				"and anything the diff appears to have forgotten. Read-only: /review",
				"never edits, stages, or commits.",
				"",
				"With no changes to review it says so rather than inventing findings.",
			},
			Handler: handleReviewCommand,
		},
		{
			Name: "/commit", Usage: "/commit [message]", Category: catDev,
			Summary: "Commit staged work, writing the message from the diff",
			Detail: []string{
				"With a message, commits with it. Without one, reads the staged diff and",
				"proposes a Conventional Commits message for you to accept, edit, or",
				"reject.",
				"",
				"Nothing is staged for you: what you staged is what gets committed. The",
				"commit is journalled, so /undo can offer a soft reset afterward.",
			},
			Handler: handleCommitCommand,
		},
		{
			Name: "/git", VoiceOK: true, Usage: "/git <request>", Category: catDev,
			Summary: "Natural-language git operations with safety gates",
			Detail: []string{
				"Describe the outcome in plain English. Destructive operations — force",
				"push, hard reset, worktree clean, deleting the main branch — require a",
				"typed confirmation phrase and can never be confirmed by voice.",
			},
			Handler: handleGitCommand,
		},
		{
			Name: "/web", VoiceOK: true, Usage: "/web <query|url>", Category: catDev,
			Summary: "Guarded web search or page fetch",
			Detail: []string{
				"An argument that parses as an http(s) URL is fetched; anything else is",
				"searched. Fetches are restricted to public addresses: private and",
				"link-local ranges are refused, so a URL cannot be used to probe the",
				"local network.",
				"",
				"Retrieved text is data with no authority — it is never treated as an",
				"instruction, whatever the page says.",
			},
			Handler: handleWebCommand,
		},
		{
			Name: "/explain", VoiceOK: true, Usage: "/explain <command|technique>", Category: catDev,
			Summary: "Defensive analysis: techniques, detections, mitigations",
			Handler: handleExplainCommand,
		},
	}
}

// legacyCommands holds the sections that predate the registry: providers,
// knowledge base, security tooling, voice, and the danger zone.
func legacyCommands() []command {
	return append(append(append(append(append(
		providerCommands(),
		knowledgeCommands()...),
		securityCommands()...),
		voiceCommands()...),
		utilCommands()...),
		dangerCommands()...)
}

func providerCommands() []command {
	return []command{
		{
			Name: "/provider", VoiceOK: true, Usage: "/provider [status|list|use <name>|<name>]", Category: catAI,
			Summary: "Switch or inspect the AI provider",
			Detail: []string{
				"A bare provider name is the same as \"use <name>\".",
				"Switching probes the new provider and says whether it can actually",
				"answer, rather than reporting only that a key is present.",
			},
			Handler: handleProviderCommand,
		},
		{
			Name: "/provider-status", VoiceOK: true, Category: catAI,
			Summary: "Provider health, keys, failover state, planner transport",
			Handler: func(cmdArgs) { handleProviderStatus() },
		},
		{
			Name: "/model", VoiceOK: true, Usage: "/model [list|use <id>|<id>]", Category: catAI,
			Summary: "Switch or list models on the active provider",
			Handler: handleModelCommand,
		},
		{
			Name: "/models", VoiceOK: true, Category: catAI,
			Summary: "List models the active provider offers",
			Handler: func(cmdArgs) { listAvailableModels() },
		},
		{
			Name: "/test-basic-ai", VoiceOK: true, Category: catAI,
			Summary: "Smoke test the active AI model",
			Handler: func(cmdArgs) { testBasicAI() },
		},
	}
}

func knowledgeCommands() []command {
	return []command{
		{
			Name: "/rag-status", VoiceOK: true, Category: catKnowledge,
			Summary: "RAG indexing progress and vector stats",
			Handler: func(cmdArgs) { handleRAGStatus() },
		},
		{
			Name: "/rag-reindex", VoiceOK: true, Category: catKnowledge,
			Summary: "Trigger a background RAG reindex",
			Handler: func(cmdArgs) { handleRAGReindex() },
		},
		{
			Name: "/rag-rebuild", VoiceOK: true, Category: catKnowledge,
			Summary: "Force a full RAG knowledge base rebuild",
			Handler: func(cmdArgs) { handleRAGRebuild() },
		},
		{
			Name: "/rag-reset", Category: catKnowledge,
			Summary: "Wipe all RAG vector data",
			Handler: func(cmdArgs) { handleRAGReset() },
		},
		{
			Name: "/knowledge-update", VoiceOK: true, Category: catKnowledge,
			Summary: "Fetch latest CVEs, CISA KEV, exploits, MITRE ATT&CK",
			Detail: []string{
				"Cancellable with Ctrl+C. Set NVD_API_KEY to cut the initial sync from",
				"roughly 40 minutes to 10.",
			},
			Handler: func(cmdArgs) { handleKnowledgeUpdate() },
		},
		{
			Name: "/knowledge-status", VoiceOK: true, Category: catKnowledge,
			Summary: "Knowledge database row counts and last update",
			Handler: func(cmdArgs) { handleKnowledgeStats() },
		},
		{
			Name: "/knowledge-reindex", VoiceOK: true, Category: catKnowledge,
			Summary: "Rebuild the FTS5 search index",
			Handler: func(cmdArgs) { handleKnowledgeReindex() },
		},
	}
}

func securityCommands() []command {
	return []command{
		{
			Name: "/vuln", VoiceOK: true, Aliases: []string{"/intel"}, Usage: "/vuln <CVE|EDB|T-ID|query>",
			Category: catSecurity,
			Summary:  "Defensive vulnerability intelligence lookup",
			Detail: []string{
				"Answers from the local database first. A CVE outside the rolling",
				"119-day window is fetched on demand from NVD and cached.",
			},
			Handler: handleVulnCommand,
		},
		{
			Name: "/scan", Usage: "/scan [authorize|revoke|status] <target>", Category: catSecurity,
			Summary: "Reconnaissance against an authorized target",
			Detail: []string{
				"A target must be authorized with a written scope before it can be",
				"scanned: /scan authorize <target> --reason \"<scope>\". The reason is the",
				"record that this was a permitted engagement.",
				"",
				"revoke withdraws an authorization; status lists the current ones.",
			},
			Handler: handleQuickScan,
		},
		{
			Name: "/sandbox", VoiceOK: true, VoiceReadOnly: true,
			Usage: "/sandbox [off|current|strict]", Category: catSecurity,
			Summary: "Directory confinement for every executed command",
			Detail: []string{
				"off      no confinement",
				"current  commands are confined to the current directory tree",
				"strict   current-dir confinement plus kernel confinement where the",
				"         platform provides it (see /doctor for the live backend)",
			},
			Handler: handleSandboxCommand,
		},
		{
			Name: "/stealth", Usage: "/stealth <on|off>", Category: catSecurity,
			Summary: "Private history mode (suppresses shell history)",
			Detail: []string{
				"Suppresses the child shell's history and Helix's own on-disk history.",
				"The full safety pipeline still applies — private execution is not an",
				"escape hatch, and the sandbox still confines every command.",
			},
			Handler: handleStealthCommand,
		},
		{
			Name: "/crash", VoiceOK: true, Usage: "/crash [list|view <n>|clear]", Category: catSecurity,
			Summary: "Inspect and manage local crash diagnostics",
			Detail: []string{
				"Crash reports are local-only and telemetry-free. Keys, tokens, and",
				"secrets are redacted before anything is written.",
			},
			Handler: handleCrashCommand,
		},
	}
}

func voiceCommands() []command {
	return []command{
		{
			Name: "/blackbox", VoiceOK: true, Aliases: []string{"/bb"},
			Usage: "/blackbox [on|off|status|setup|look|eyes|wake|tts|say|log|stats]", Category: catVoice,
			Summary: "Live mode — Helix listens, watches, answers, and speaks up",
			Detail:  blackBoxDetail(),
			Handler: handleBlackBoxCommand,
		},
		{
			Name: "/listen", VoiceOK: true, Usage: "/listen [seconds]", Category: catVoice,
			Summary: "Record and transcribe one clip (max 60s)",
			Handler: handleListenCommand,
		},
		{
			Name: "/mictest", VoiceOK: true, Category: catVoice,
			Summary: "3s self-test: is the mic actually being heard?",
			Handler: func(cmdArgs) { handleMicTest() },
		},
	}
}

func utilCommands() []command {
	return []command{
		{
			Name: "/audio", VoiceOK: true, Usage: "/audio <on|off>", Category: catUtil,
			Summary: "Toggle tonal audio feedback",
			Handler: handleAudioCommand,
		},
		{
			Name: "/typewrite-all", VoiceOK: true, Usage: "/typewrite-all <on|off>", Category: catUtil,
			Summary: "Typewriter effect for ALL output, not just AI replies",
			Handler: handleTypewriteAllCommand,
		},
	}
}

func dangerCommands() []command {
	return []command{
		{
			Name: "/purge", Category: catDanger,
			Summary: "Wipe ALL Helix data (keys, DBs, caches) for a fresh start",
			Detail: []string{
				"Irreversible. Removes API keys, the knowledge database, RAG indexes,",
				"conversation memory, archived sessions, tasks, and crash reports.",
				"Requires a typed confirmation.",
			},
			Handler: func(cmdArgs) { handlePurgeCommand() },
		},
		{
			Name: "/reboot", Category: catDanger, Usage: "/reboot [now|check]",
			Summary: "Update if there is one, then restart where and how you left it",
			Detail: []string{
				"Checks for a newer Helix, offers to install it, and restarts. The",
				"process is replaced, so open database handles, loaded models and",
				"cached provider state are genuinely gone — which is what /purge and",
				"a provider change ask you to restart for.",
				"",
				"/reboot        check for an update, then restart",
				"/reboot now    restart immediately, no update check",
				"/reboot check  only check — do not install, do not restart",
				"",
				"Updates come from the project's GitHub releases and from a Helix you",
				"built yourself (dist/helix), and INSTALL AUTOMATICALLY — no prompt,",
				"typed or spoken. A download is installed only if its SHA-256 matches",
				"the release's own checksums file and the binary really is Helix for",
				"this machine. The previous binary is kept, and restored automatically",
				"if the new one cannot start.",
				"",
				"What survives a restart: the conversation, your in-progress tasks, the",
				"working directory, the active provider and model, and the mode.",
				"Rebooting from live mode comes back in live mode; from the keyboard,",
				"at the keyboard.",
				"",
				"Voice-reachable: say \"reboot\" or \"please reboot\", which installs",
				"too. /reboot check reports without installing or restarting, and",
				"update.check: false turns the check off entirely.",
				"",
				"Configure with the \"update\" section of ~/.helix/config.json:",
				"channel (auto|github|local|off), repo, check, local_paths.",
			},
			// VoiceOK by owner decision, alongside the "manual mode" safety
			// valve. It is the one DANGER ZONE command that destroys nothing:
			// the record is written BEFORE the process ends, so a misheard
			// reboot costs a few seconds and resumes exactly where it was.
			VoiceOK: true,
			Handler: handleRebootCommand,
		},
	}
}

// hookEventList is the /hooks help line naming every valid event, derived from
// the hooks package so the two cannot drift.
func hookEventList() string {
	names := make([]string, 0, len(hooks.Events()))
	for _, e := range hooks.Events() {
		names = append(names, string(e))
	}
	return strings.Join(names, ", ")
}

// permissionModeList is the /permissions help line, derived from the agent's
// own mode table.
func permissionModeList() string {
	names := make([]string, 0, len(agent.PermissionModes()))
	for _, m := range agent.PermissionModes() {
		names = append(names, string(m))
	}
	return strings.Join(names, " | ")
}
