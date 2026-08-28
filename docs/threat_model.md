# Helix Threat Model — Instruction Firewall (Phase 12)

## Threat
Indirect prompt injection: hostile text inside retrieved knowledge (poisoned
MAN pages, compromised feeds, future user documents) attempts to hijack the
planner into executing attacker-chosen commands.

## Controls (five layered, independently-failing)
1. **Structured-fields-only context** — raw retrieved text never reaches the
   planner; only Name/Synopsis/Options/Examples + a sanitized (<=200 rune)
   description, wrapped in `<retrieved_data authority="data-only">` with an
   explicit zero-authority rule.

   *Scope note (2026-08-23): retrieved knowledge is no longer the only untrusted
   block.* Four others now ride into planner prompts under the same fence and the
   same sanitizer — session history, the `/todo` list, project context
   (`HELIX.md`/`AGENTS.md`/`CLAUDE.md`), and, in agentic mode, the execution
   report carrying a bounded tail of what each step printed. Every one is content
   Helix did not author: a committed project file is written by whoever wrote the
   repository, and command output is fully attacker-controllable through a
   crafted filename or a poisoned log line. `docs/harness.md` §6 lists them with
   their bounds. Session turns additionally carry provenance — a transcript the
   Voice Risk Policy refused to act on is labelled `not understood`, so the model
   can tell a confident quotation from a guess Helix declined to trust.
2. **Sanitization** — invisible/bidi Unicode, markdown fences, backticks, JSON
   braces, and imperative patterns ("ignore previous instructions", "you must",
   "run curl …", pipe-into-shell) are stripped or filtered.
3. **Canary honeypot** — a per-request random token embedded in the data block;
   its appearance in model output aborts with an injection alert.
4. **Critic pass (risk-gated)** — a low-temperature, strict-JSON call seeing
   only the user request + proposed commands; triggered exclusively by
   unsolicited external URLs; "no"/garbage/unreachable all quarantine. A
   *non-answer* is reported differently from a rejection while quarantining
   identically: the security property is unchanged, but a critic that returned
   nothing must not read to the user as a judgement on their request. That
   distinction exists because a token budget too small to hold the verdict once
   made every reviewed plan look refused.
5. **Provenance escalation** — any plan command carrying a URL/host/path token
   present in retrieved context but absent from user input is forced to
   Medium risk (mandatory confirmation).

## The voice channel is a different threat, deliberately
A transcript is **not** data-only. Once speech is transcribed it becomes text
with user authority, which is precisely what this firewall does not cover: a
TV, a podcast, or a person in the room becomes an instruction. That surface has
its own model and its own controls (risk capped at Medium, typed confirmations
structurally unreachable by voice, fail-closed prompts, a wake lockout) in
`docs/threat_model_voice.md`. One control belongs here because it is a routing
decision rather than a voice one: spoken input never takes the
high-confidence-shell fast path, so a sentence whose first word happens to be a
command name reaches the planner instead of the OS.

One command in the DANGER ZONE category is deliberately reachable by voice:
`/reboot`, which restarts the shell. It qualifies because it destroys nothing —
the continuity record is written before the process ends, so a misheard trigger
costs seconds rather than data. Everything else in that category remains
unreachable, and the criterion is data loss rather than how alarming the command
sounds. Its self-update half installs automatically, by owner decision, from the
microphone as well as the keyboard — a deliberate trade recorded in ADR-019 and
V5e rather than an oversight. See `threat_model_voice.md` rules 9 and V5e.

## Residual Risk (honest statement)
Distinguishing instruction from data in natural language is undecidable in the
general case; a sufficiently subtle injection can still pass all five controls.
The firewall therefore does not claim to "solve" prompt injection — it makes a
successful attack require defeating five independent layers, and limits blast
radius via the existing safety pipeline (validation, risk tiers, sandbox,
typed confirmations).