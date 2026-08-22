# The Helix Agentic Harness

This document covers the layer between "the model produced a plan" and "the
machine did something": the tool vocabulary, the approval posture, the task
list, local policy hooks, and the context Helix carries between turns.

Everything here is *additive to* the safety pipeline described in
[`docs/threat_model.md`](threat_model.md). Nothing in this document can relax
validation, the risk tiers, the sandbox, typed confirmations, or the Voice Risk
Policy.

---

## 1. The turn

```
input ──▶ classify ──▶ [slash command?] ──▶ dispatch, done
                │
                ├──▶ [high-confidence shell?] ──▶ safety pipeline ──▶ exec
                │
                └──▶ retrieve ──▶ plan ──▶ firewall ──▶ prepare ──▶ per-step:
                                                                     tier gate
                                                                     sandbox
                                                                     pre-hook
                                                                     exec
                                                                     post-hook
```

With `/agentic on`, a failing step feeds its **observed** outcome — exit status
and a bounded, sanitized tail of its output — back to the planner, which replans.
That loop re-enters the whole pipeline on every iteration; it is bounded by the
step budget (`/agentic steps <n>`, 1–20) and cannot skip a gate.

`/plan <request>` runs the left half only: planner, firewall canary check, parse,
safety rewrite — then prints the steps and stops.

---

## 2. Tool vocabulary

The planner's tool set is **closed**. A step naming anything outside it is
dropped from the plan, never dispatched. `/tools` prints the live list with each
tool's gate and whether it is usable right now.

| Tool | Does | Gate |
| :--- | :--- | :--- |
| `response` | Answers in prose | none — text only |
| `shell` | Runs a shell command | validation → risk tiers → sandbox → hooks |
| `git` | Repository operations | typed confirmation for destructive actions; never by voice |
| `package` | Install / update / remove | package safety check → confirmation |
| `recon` | Scans a target | written-scope authorization required |
| `web` | Search or fetch a public page | public-address guard; retrieved text has zero authority |
| `vision` | Looks through the camera and describes one frame | `/eyes` opt-in; one in-memory frame per turn, never written to disk |

Adding a tool widens what Helix can do by exactly that capability. It does not
loosen the gate in front of the others.

---

## 3. Approval posture — `/permissions`

One setting for "how much may happen without asking".

| Mode | Low risk | Medium risk | High risk |
| :--- | :--- | :--- | :--- |
| `plan` | printed, not run | printed, not run | printed, not run |
| `cautious` | asks | asks | blocked |
| `ask` *(default)* | runs | asks | blocked |
| `auto` | runs | runs, announced | blocked |

The mode is a **filter on the question**, not on the gates:

- High risk is blocked in every mode. There is no posture that unblocks it.
- Typed confirmations stay typed. `auto` does not answer "YES, FORCE PUSH".
- The sandbox validates every command in every mode.
- Voice-originated plans stay capped at medium risk regardless of posture.
- Hooks still run, and a blocking one can still refuse.

`/permissions auto` asks for confirmation before it takes effect, because it is
the only mode that removes a prompt the user would otherwise have seen. The
choice persists to `~/.helix/config.json`, and a non-default posture is announced
at startup — a session that silently runs more than you expect is the failure
this avoids.

`/dry-run` is the narrower tool: the planner still plans and the pipeline still
validates, only the final exec is skipped. `plan` mode stops earlier and prints
the plan instead.

---

## 4. Task list — `/todo`

A persisted list in `~/.helix/todo.json` (0600). It is not a notepad: the **open**
tasks are injected into every planner prompt as a zero-authority fenced block, so
a multi-turn task can be picked up where the last turn stopped.

```
/todo add migrate the config loader
/todo start 1
/todo block 1 waiting on the schema decision
/todo done 1
/todo prune           # drop completed tasks, keep the IDs you just read
```

Completed tasks are excluded from the injected block — presenting finished work
as outstanding invites the planner to redo it.

---

## 5. Local policy hooks — `/hooks`

Hooks run your own commands around tool execution. They exist for policy Helix
cannot know about: *this* machine, *this* repository, *this* team.

```jsonc
// ~/.helix/hooks.json          (0600)
{
  "hooks": [
    {
      "name": "protect-prod",
      "event": "pre-shell",
      "match": "kubeconfig\\.prod|--context prod",
      "command": "echo 'production access is manual only' >&2; exit 1",
      "blocking": true
    },
    {
      "name": "gofmt-after-write",
      "event": "post-shell",
      "match": "\\.go\\b",
      "command": "gofmt -l ."
    }
  ]
}
```

**Events**: `pre-shell`, `post-shell`, `pre-git`, `post-git`, `session-start`,
`session-end`. `/hooks events` prints them; `/hooks test <event> <command>` runs
one once with the hook environment populated, so a rule can be checked before it
is trusted to block real work.

**Fields**: `name` (required, the handle for `/hooks rm`), `event`, `command`,
optional `match` (Go regexp against the command or git action; absent = every
occurrence), `blocking`, `timeout_sec` (default 30), `disabled`.

**Environment** the command receives — nothing is interpolated into the command
string:

| Variable | Meaning |
| :--- | :--- |
| `HELIX_HOOK_EVENT` | the event that fired |
| `HELIX_TOOL` | `shell`, `git`, `session` |
| `HELIX_ACTION` | planner action, where the tool has one |
| `HELIX_COMMAND` | the command (or git action) in question |
| `HELIX_CWD` | working directory |
| `HELIX_EXIT_CODE` | post-* only |
| `HELIX_ERROR` | post-* only |

### Security model

Hooks are trusted local configuration, and that is a bounded decision:

1. **Provenance.** Hooks come from `~/.helix/hooks.json` and nowhere else.
   Nothing a model produces, and nothing retrieved from the network, can define
   or edit one. A planner that wanted to disable a hook would have to write that
   file — itself a shell step subject to the full pipeline.
2. **No interpolation.** The step's details reach the hook as environment
   variables. Splicing a model-authored command into a hook's shell line would
   make every hook an injection site; it is never done.
3. **Subtract only.** Hooks run *after* every built-in gate has already
   approved the step. A blocking `pre-shell` or `pre-git` hook that exits
   non-zero denies it. A hook can never approve something the risk tiers
   rejected, because it never sees it.
4. **Fail closed.** A blocking pre-hook that times out, or whose interpreter is
   missing, denies the step. A hook that could not run has approved nothing.
5. **Post-hooks cannot deny.** The action already happened; turning a reporting
   hook's exit code into a step failure would misattribute the outcome.
6. **Loud on breakage.** A malformed `hooks.json` fails the whole load, and
   both startup and `/doctor` say plainly that **no** hooks are active. Silently
   skipping a bad rule is how a hook someone believes is guarding them turns out
   never to have run.

---

## 6. Context — what the model is told

Four blocks ride into every planner prompt. All four are fenced as
`authority="data-only"` and sanitized with the same routine as retrieved
knowledge: no fences, no backticks, bounded length. They inform the planner; they
can never instruct it.

| Block | Source | Bound |
| :--- | :--- | :--- |
| Retrieved knowledge | MAN pages, CVE/MITRE corpus | per-request retrieval |
| Session history | recent conversation turns | 10 turns, 160 chars each |
| Task list | open `/todo` items | 10 items |
| Project context | `HELIX.md` / `AGENTS.md` / `CLAUDE.md` | 16 KB read, 6 KB injected |

`/context` shows the live size of each, with estimated token counts.

Project context is discovered by walking **up** from the working directory, so a
subdirectory still finds the repository's notes, and a nested file overrides the
root. It is fenced like everything else for a specific reason: a file committed
to a repository is content written by whoever wrote that repository, which is
exactly the provenance the Instruction Firewall exists for.

Slash commands are **not** recorded as conversation turns. They are control
input; recording them put lines the user never said to the model into its
context.

---

## 7. Session lifecycle

| Command | Effect |
| :--- | :--- |
| `/memory` | show the conversation ring |
| `/clear` | archive → wipe → reset the usage meter → clear the screen |
| `/compact [focus]` | archive → replace the conversation with a model-written summary |
| `/resume [id]` | list archives, or load one (archiving the current first) |
| `/export [path]` | write a Markdown transcript |
| `/cost` | model traffic, by purpose |

Every wipe archives to `~/.helix/sessions/<timestamp>.json` (0600) first. A
transcript is cheap to keep and impossible to get back, and `/clear` is exactly
what people reach for when a session has gone wrong.

Exported transcripts quote the conversation as Markdown blockquotes, so content
containing its own headings or fences cannot restructure the document around
itself.

### On the numbers

`/cost` and `/context` label their token figures as **estimates**, computed at
roughly four characters per token. No provider in the registry returns a usage
block on the streaming path Helix uses, so an exact count is not available to
report. Call counts, failure counts, character counts, and latency are exact.

Helix ships no price table. Rates change without notice, and a confidently wrong
currency figure is worse than an honest token count.

---

## 8. Repository commands

| Command | Notes |
| :--- | :--- |
| `/init` | Surveys the repo and writes `HELIX.md`. Never overwrites without `--force`, and always shows the file before writing. |
| `/diff` | Read-only. Also lists untracked files, which `git diff` omits — the usual way a new file misses a commit. |
| `/review` | Read-only. Reviews only what changed; says so when a large diff was truncated. |
| `/commit` | Drafts a Conventional Commits message from the staged diff. Stages nothing for you. Journalled, so `/undo` can offer a soft reset. |
| `/undo` | Offers the latest journalled reversal, run through the full pipeline. |

`/commit` goes through the planner's git tool rather than a raw shell command, so
it keeps that path's confirmations, journalling, and hooks.

---

## 9. Files

| Path | Contents | Mode |
| :--- | :--- | :--- |
| `~/.helix/config.json` | preferences, provider, posture | 0644 |
| `~/.helix/session.json` | live conversation ring | 0600 |
| `~/.helix/sessions/` | archived conversations | 0600 |
| `~/.helix/exports/` | exported transcripts | 0600 |
| `~/.helix/todo.json` | task list | 0600 |
| `~/.helix/hooks.json` | local policy hooks | 0600 |
| `<repo>/HELIX.md` | project context | 0644 |

`/purge` removes all of them.
