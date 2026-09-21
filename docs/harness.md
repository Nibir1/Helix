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
                ├──▶ [high-confidence shell? TYPED only] ──▶ safety pipeline ──▶ exec
                │
                └──▶ retrieve ──▶ plan ──▶ firewall ──▶ prepare ──▶ per-step:
                                                                     tier gate
                                                                     sandbox
                                                                     pre-hook
                                                                     exec
                                                                     post-hook
```

**The fast path is typed-only.** When you type a confidently recognised command
line it runs as typed — that is the point of a shell that does not nag. Spoken
input never takes that branch: the classifier decides on the first token, and
ordinary sentences begin with command names, so "make a new branch called test"
was being executed as `make a new branch called test` instead of reaching the
planner that would have produced `git checkout -b test`. Voice always goes down
the right-hand path. The safety pipeline covered both branches throughout, so
this is a routing correction rather than a closed hole.

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
| `file` | Read, list, glob, grep, edit and write files | sandbox root → risk tiers (edit/write are medium) → hooks |
| `todo` | Keep the task list current while it works | may redirect any task; may delete only its own |
| `web` | Search or fetch a public page | public-address guard; retrieved text has zero authority |
| `vision` | Looks through the camera and describes one frame | `/blackbox eyes` opt-in; one in-memory frame per turn, never written to disk |

Adding a tool widens what Helix can do by exactly that capability. It does not
loosen the gate in front of the others.

### The `file` tool

Six actions: `read`, `list`, `glob`, `grep`, `edit`, `write`.

**Why it exists.** Until it did, a plan could only touch a file through `shell`,
and the planner prompt said so in as many words — *"For in-place file editing on
macOS, use: `sed -i '' 's/OLD/NEW/g' FILE`"*. That is a bad instruction to give a
model, and not because sed is bad. **An in-place sed whose pattern does not match
exits 0 and changes nothing**, so the model is told the edit succeeded when the
file is untouched, reports the work as done, and the next step builds on a change
that was never made. It cannot tell one occurrence from six. Quoting real code
through a shell line means escaping it twice.

`file/edit` replaces an exact snippet and **fails** when the snippet is absent,
saying so; fails when it is ambiguous, saying how many times it appeared and how
to disambiguate; and reports the number of replacements it made. The planner is
now told to use it and told why.

| Action | Args | Tier |
| :--- | :--- | :--- |
| `read` | `path` | low — 20 KB cap, binaries and directories refused |
| `list` | `path` (default `.`) | low — 200 entries |
| `glob` | `pattern`, `path` | low — 200 results, `**` supported, newest first |
| `grep` | `pattern`, `path` | low — case-insensitive, 60 matches, `file:line:` |
| `edit` | `path`, `old_string`, `new_string`, `replace_all` | **medium** |
| `write` | `path`, `content` | **medium** |

Reads are low because they change nothing; edits and writes are medium because
they change the machine, so under the default `ask` posture a write asks exactly
as a medium-risk shell command does. Nothing here is high — a path that *would*
be is refused by the sandbox before a tier is consulted.

Four properties are worth stating because they are the difference between this
and shelling out:

1. **Confinement is the sandbox's, not the tool's.** Every path goes through
   `DirectorySandbox.ValidateSafePath` — the same check `shell` gets, resolving
   symlinks on both sides, folding case for macOS and Windows, and validating a
   not-yet-existing file by its parent. The tool package has no path check of its
   own and refuses to run without a resolver; a second, weaker copy of a
   confinement rule is how a jail grows a door.
2. **Writes are atomic and preserve permissions.** A temp file in the same
   directory and one rename, with the existing file's mode carried over. A crash
   mid-write leaves the original intact, and a 0600 file does not quietly become
   0644.
3. **A near-miss filename is refused.** Creating `hepers.go` beside an existing
   `helpers.go` is almost always a path mistake; left alone it writes a stray
   file, reports success, and the edit is nowhere. The refusal names the file
   that was probably meant.
4. **A read that finds nothing does not abort the plan.** Every other tool stops
   the remaining steps on error, because they usually depend on the one that
   failed. A read is different: the model asked precisely because it did not
   know, and "no such file" is the answer. Aborting spent a whole planner round
   trip on that discovery — a real run lost two thirds of a four-iteration
   budget to a `read package.json` that cancelled the rest of its own plan.
   `edit` and `write` failures still abort: a change that failed may have left
   the tree in a state the following steps assumed away.
5. **Everything a file tool returns is data.** Contents come back through the
   same `authority="data-only"` execution report as command output. A file in a
   repository is content written by whoever wrote that repository, which is
   exactly the provenance the Instruction Firewall exists for.

**An empty search result is an answer, not a failure.** A real session asked for
a file that did not exist and the model globbed eight times for it — the same
pattern twice, then progressively looser ones, then `**/*.py` in a Go repository
— before concluding what the first result had already said. The loop directive
now says so explicitly when a search came back empty, because "do not re-run a
search that already succeeded" did not cover it: a search that found nothing
does not feel like a success.

Generated and vendored trees — `.git`, `node_modules`, `vendor`, `dist`,
`target`, `__pycache__` and the rest — are pruned from `glob` and `grep`. A
search that spends its match budget inside `node_modules` has answered a question
nobody asked.

The implementation is ported from Synapse's agent loop; the gating, the sandbox
wiring and the data-only fencing are Helix's.

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

**The table is only as good as the tier each command gets**, which is a
different package (`internal/commands/safety`) and was wrong in two ways until
2026-09-08. Under the default `ask` posture, Low **runs**, so a command graded
Low is a command that happens without a question — and redirection was graded by
testing for `" > "` with a space on each side. `echo x > f` asked; `echo x >f`
ran. `echo key >~/.ssh/authorized_keys` ran. Separately, the hard block against
redirecting onto a raw disk device had a mis-escaped pattern and had never
matched anything, so `cat /dev/zero > /dev/sda` reached the tiers at all rather
than being refused before them. Both are fixed and pinned by behaviour tests;
the detail is in `SECURITY.md` §1. Worth keeping in mind when reading any row
above: this table describes what a posture does with a tier, and says nothing
about whether the tier is right.

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
tasks are injected into every planner prompt, and the agent can now write back.

```
/todo add migrate the config loader
/todo start 1
/todo block 1 waiting on the schema decision
/todo done 1
/todo open 1          # also un-supersedes something Helix set aside
/todo prune           # drop completed tasks, keep the IDs you just read
```

Completed and superseded tasks are excluded from the injected block — presenting
finished work as outstanding invites the planner to redo it, and re-presenting a
task the agent already set aside invites it to re-litigate its own decision.

### The list has two authors

A plan written before any work happens is a guess, and the agent is the party
that finds out it was wrong: it reads the code, runs the tests, and discovers
that step 3 is already done and step 4 has to happen first. Until it could write
here, it had no way to say so — and no way to record work it decided was needed,
so on a long job the real plan lived inside one planner call and was re-derived
from scratch on the next.

So it writes here now, through a `todo` tool with four actions:

| Action | Does |
| :--- | :--- |
| `add` | record work it discovered is needed |
| `revise` | rewrite a task that is right in spirit, wrong in detail |
| `state` | move a task, including `done` and `superseded` |
| `drop` | delete a task — **its own only** |

**The rule is: it may redirect anything and erase only its own.**

That is not a judgement about whose plan is better; you asked for this precisely
because a human plan can be wrong. It is that deletion is the only one of those
operations that cannot be seen or undone. A superseded task stays in `/todo`,
carries the reason it was set aside, and comes back with `/todo open <id>`. A
deleted one is a task you still believe is tracked.

Revision follows the same principle. When the agent rewrites something you wrote,
your original wording is kept and `/todo` shows it:

```
  2 · bump the version in internal/config/config.go
      you wrote: bump the version in package.json
      helix: this is a Go repo; there is no package.json
```

The agent gets to change the plan. It does not get to change the record of what
you asked for.

**A reason is required** whenever it revises, completes or supersedes a task you
wrote. An overrule with no stated reason is indistinguishable from a mistake.
Progress — marking your task `in_progress` — needs none: it says nothing you
would dispute, and demanding a reason for it only trains the model to emit
filler. Its own tasks are its own business.

Every edit is **announced on screen as it happens**, in the same shape `/todo`
uses — state marker, id, what just happened, then the reasons hanging in a
column:

```
  ▸ TASK 1   started    add empty-input validation to parser.go
                        ↳ why       starting on the panic fix first
  · TASK 2   rewrote    bump the version in version.go to 1.2.0
                        ↳ you wrote bump the version in package.json to 1.2.0
                        ↳ why       there is no package.json; this is a Go module
  ⊘ TASK 6   set aside  add a retry loop to transport.go
```

An edit you only discover later by typing `/todo` is a plan that changed behind
your back.

`/todo` itself reads as an instrument: open work above settled work, one marker
per state so the live task is findable, the author at the right edge, and a
meter that reports only the states that are not empty.

```
  │ ▸  2  bump the version in version.go to 1.2.0
  │       ↳ you wrote bump the version in package.json to 1.2.0
  │       ↳ why       there is no package.json here; this is a Go module
  │ ·  4  update the CHANGELOG for the bump                            helix
  │
  │ ✔  1  add empty-input validation to parser.go
  │ ⊘  6  add a retry loop to transport.go
  │
  │ ▓▓▓▓▓░░░░░░░░░░░  1 in progress · 1 pending · 1 done · 1 superseded   2 of 4 settled
```

### It steers the loop

The agentic loop used to stop when the last batch of steps exited 0. That is not
the same as the work being finished: an agent that writes a five-step plan and
completes step one was stopped there, with four steps it had declared necessary
left undone and nothing reporting it. The loop now also continues while the agent
has an open task **of its own, created this turn** — three conditions, each
load-bearing:

- *created this turn*, so a task left open by an earlier run cannot make an
  unrelated question spend twenty planner calls finishing yesterday's job;
- *the agent's own*, because your list is not a work queue — "renew the domain"
  sitting in `/todo` must never become something the harness attempts;
- *not settled*, where superseded counts as settled because the agent itself
  decided that task should not happen.

"The agent's own" means **created or touched this turn**, and the second half
matters more than the first. The rule was originally "tasks the agent created",
and the first real run broke on it: the agent worked through three tasks the
*user* had written, created none of its own, and so reported no outstanding work
at every iteration. The loop never said it was working, the budget ran out, and
the turn ended with a task in progress whose edit was never made. Marking a task
`in_progress` is the agent **adopting** it — that is the moment it becomes this
run's work, whoever wrote it down.

A task nobody touched this turn is still ignored, so the property that rule
existed for survives: "renew the domain" sitting in `/todo` never becomes
something the harness attempts.

**A task whose work you did is `done`, never `superseded`.** Superseded means the
task should not happen; done means it has, including when the agent is the reason.
A real run edited `version.go` successfully and then set the task aside as
"already in place" — it having been the thing that put it there — which reads as
work that was never needed.

**A verification task is not closed on stale evidence.** "Run the tests" may only
be marked done from a result gathered *after* the last change — if the tests ran
and then a file was edited, that run says nothing about the current state. The
rule is in the planner prompt and repeated in the loop's directive, because by
round three the directive is what the model is actually reading. It came from a
real run that closed "run the tests" on a result predating its own edit, and
reported honestly that it had done so.

### Chrome carries no label

A tool step is a record of what happened, not something Helix is saying, so it
carries no bracketed label and starts where every other line starts:

```
  ❯ Tell me what the parser file is doing
  ▸ glob **/*parser*
  ┄ answering 1/3  reading retrieved results
  ▸ read parser.go
```

`[EXEC] glob **/*.md` was the last bracketed label left in a live trace, and it
sat at column **zero** while the step markers, the prompt and the reply band all
start at column two — so the left edge of a running session broke in and out by
two cells, line by line.


Step markers and phase lines are the same family. They used to be
`--- Step 1 ---` and `HELIX :: ANSWERING :: reading retrieved results (1/1)`;
rewriting the text alone was not enough, because routed through
`PrintSystemMessage` they came out as `[SYSTEM]    ┄ step 1 of 3` — the new line
inside the old frame, which reads worse than either alone. `PrintChrome` is the
channel that adds nothing.

### It says what it is doing

In a live conversation the screen is the thing you are not looking at. A
multi-step job ran for half a minute emitting `EXEC` lines nobody heard, and the
only spoken output was the final reply — so from the listener's side Helix went
quiet with no sign that it had understood, started, or finished. Each edit to
the plan now gets one short spoken line:

| When | Spoken |
| :--- | :--- |
| the plan is written | *"Right — 3 steps. First, read parser.go."* |
| a task starts | *"Now: bump the version."* |
| a task finishes | *"Done. Next: run the tests."* |
| a task is set aside | *"Skipping the retry loop — the reason is on screen."* |
| your task is rewritten | *"Changing one of your tasks: … The reason is on screen."* |
| the run ends | *"All 3 done. The details are on screen."* |

Short is the design. These interrupt a conversation, so nothing spoken carries a
path, a line number or a reason — an absolute path is reduced to its base name
and the detail stays on screen where it can be read. The plan is announced
**once**, naming the first task and counting the rest: reading five tasks aloud
is a list nobody retains, while the first task plus a count tells you Helix
understood and how long this will take.

Adds are silent for the same reason — a three-task plan arrives as three add
steps, and narrating each is three interruptions for one decision.

### Every run says how it ended

A run that stops prints why, in all four cases:

```
Done — 2 tasks closed.
```
```
Step budget reached (4 follow-ups) with work still open. Nothing was lost — the tasks are on the list.
      #2 [in_progress] bump the version constant to 1.2.0 in version.go
      /todo shows the list · /agentic steps <n> raises the budget
```
```
Stopped with work still open.
      #2 [in_progress] bump the version constant to 1.2.0 in version.go
```
```
Stopped with an unresolved error after 4 follow-ups.
```

This is not decoration. The first real run ended by printing *nothing at all* —
the last iteration executed, the budget ran out, the prompt came back, and the
only way to discover that a task was left half-done was to type `/todo` and read
it. "Finished" and "ran out of road" look identical from a returned prompt, and
the open tasks are named because "some work is open" is not actionable.

### Why this tool is not gated

Every other tool runs commands, changes files, reaches the network or opens a
camera. This one edits a list of sentences in a file in Helix's own state
directory. Grading it medium and asking *"may I update my task list?"* between
every step would train you to approve without reading, which is the failure
confirmations exist to prevent. Its authority is bounded by what the list can do
— nothing — and by the erase rule above.

`plan` mode still prints instead of acting, and `/dry-run` still declines to
change state.

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
    },
    {
      // A file step's subject is "<action> <path>", so a rule can key on
      // either: `^write ` gates every write, `\\.env$` gates one file
      // whatever is done to it.
      "name": "no-writes-to-secrets",
      "event": "pre-file",
      "match": "^(write|edit) .*(secrets/|\\.env)",
      "command": "echo 'secrets are edited by hand' >&2; exit 1",
      "blocking": true
    }
  ]
}
```

**Events**: `pre-shell`, `post-shell`, `pre-git`, `post-git`, `pre-file`,
`post-file`, `session-start`, `session-end`. `/hooks events` prints them; `/hooks test <event> <command>` runs
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
| `HELIX_TOOL` | `shell`, `git`, `file`, `session` |
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

### Output that is not Helix's

A package install prints Helix's lines, then hands the terminal to pip, brew,
cargo or apt, then takes it back. Nothing used to mark the handover, so a
stranger's error text sat in the middle of Helix's own report and the reader had
to work out which lines they could act on.

Helix does not reformat that output — reflowing someone's progress bar would be
worse than leaving it. It marks where the handover happens, in both directions,
and says who is talking:

```
  │ ! piper-local  not installed — installing it
  │   → pip3 install --user piper-tts
  ╷ output below is pip3's own

Collecting piper-tts
Successfully installed piper-tts-1.2.0

  ╵ pip3 finished
  │ ✔ piper-local  installed
```

The marks are deliberately not the gutter Helix's own lines carry, and the
closing one reports the verdict so a long scroll does not have to be read
backwards. The label names the program rather than its wrapper: `sudo apt-get
install` is **apt-get** talking, and `python3 -m pip` is **pip**.

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
| Task list | open `/todo` items, with ids and author | 10 items |
| Project context | `HELIX.md` / `AGENTS.md` / `CLAUDE.md` | 16 KB read, 6 KB injected |

`/context` shows the live size of each, with estimated token counts. `/memory`
shows the turns themselves, and says on the screen that they are replayed as
zero-authority data — the property is what makes replaying them safe, so the
command that displays them is the right place to state it.

Session turns carry provenance as well as text. A turn whose transcript the Voice
Risk Policy refused to act on — below the confidence gate — is labelled
`user(voice, not understood)` rather than quoted as if you had said it cleanly.
That matters because the alternative silently launders a guess into the model's
context for the next twenty turns, and Helix could then answer the misheard
version of a question nobody asked.

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
| `/reboot` | update if there is one, then restart — the conversation is **kept**, not archived |

Every wipe archives to `~/.helix/sessions/<timestamp>.json` (0600) first. A
transcript is cheap to keep and impossible to get back, and `/clear` is exactly
what people reach for when a session has gone wrong. Nothing in this table
destroys a transcript, `/reboot` included: a restart reloads the same ring from
`session.json` rather than archiving and starting over.

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
| `~/.helix/reboot.json` | `/reboot` continuity record — consumed on read, ignored past 12 h | 0600 |
| `~/.helix/update-pending` | a note to the restart supervisor that a binary was just installed | 0600 |
| `~/.helix/sessions/` | archived conversations | 0600 |
| `~/.helix/exports/` | exported transcripts | 0600 |
| `~/.helix/todo.json` | task list | 0600 |
| `~/.helix/hooks.json` | local policy hooks | 0600 |
| `<repo>/HELIX.md` | project context | 0644 |

`/doctor` opens with a **BINARY** row when the process answering you is older
than the file it was started from, or when a local `dist/` build is newer than
the binary you are running. Neither is visible from inside a running shell: a
process keeps its own image, so replacing the file changes nothing until it
restarts, and `make current` builds without installing. The row is absent when
everything agrees.

`/purge` removes everything under `~/.helix/` above, plus `~/.helix_history`. It
does **not** touch `HELIX.md`: that file lives in your repository, not in Helix's
state directory, and a wipe of Helix's own data has no business reaching into a
checkout. `/reboot` is what makes the removal take
effect — open database handles only release when the process exits, so "blank
slate" is true one restart later.

After the wipe, `/purge` asks **one more question, separately**: whether to
remove Helix itself — the binary, the `/etc/shells` registration, and the
launchd or systemd service. It is a third confirmation rather than part of the
first because `/purge` already means "clean slate so I can carry on", and
folding an uninstall into that would take the shell away from someone halfway
through re-configuring it. Say no and you keep Helix with nothing in it; say
yes and there is nothing left. The prompt is skipped entirely when there is
nothing installed to remove.

`make uninstall` and `helix uninstall` reach the same code from outside a
session, and neither needs a working build. All three restore your login shell
*before* removing the binary it points at, and if that restore fails the binary
is deliberately kept — a missing login shell is a machine that cannot open a
terminal.

A restart also leaves a turn in the conversation saying it happened — mode,
directory, provider — so the planner can answer "did you reboot?" instead of
denying it. That turn records Helix's own action and never anything the
microphone heard.

An open task survives a `/reboot` by two independent paths: `todo.json` is
reloaded like any other boot, and the continuity record separately carries the
**in-progress** task texts so the resume can name what you were in the middle of.
The resumed panel prints each task once — it deliberately does not also print the
one-line summary when that summary is just the single task restated.
