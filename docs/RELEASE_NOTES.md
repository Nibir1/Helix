## Next release (unreleased, on `feat/metabolism-integration`)

### Security

- **Reading secret material asks, in every posture.** Reading a private key,
  a cloud credential, a `.env` file or a password store was Low risk and ran
  without a prompt; its content then went to the model provider with the next
  planner round. Such reads, and any shell command naming such a file, now
  ask even under `/permissions auto` and for trusted sources. Replays never
  read them, and `helix -c` refuses them. Found by the Metabolism engine's
  poisoning suite. See SECURITY.md §1.
- **Planner steps ask again.** From v1.0.0 every step the planner proposed was
  marked trusted, so a Medium-risk `file write`, `file edit` or redirecting
  shell command ran without asking under the default `ask` posture, the daemon
  included. Planner steps are now never trusted; only Helix's own fast-path
  plans are, and `/permissions auto` is the only way to skip the question.
  See SECURITY.md §1.

### Fixes

- **Typed requests that mention a path reach the planner again.** The
  input classifier scores the whole line, so "run go test ./internal/..." or
  "summarise CLAUDE.md" scored as a shell command at full confidence and
  went straight to the shell: "command not found: run". A line now skips
  the planner only if its first word is a builtin, a known command, an
  assignment or a path, or is found on `PATH`. Found by Metabolism's
  automated days, which lost 10 of 100 turns to it.
- **The native planner path no longer wastes a round trip on every plan.**
  With DeepSeek-V4.1-Flash (thinking on by default), every planning first
  sent a native tool-calling request that the provider refused ("Thinking
  mode does not support this tool_choice"), then planned through the prompt
  path: one refused request per planning, about half of all recorded model
  calls. A 400 now switches the native path off for that provider and model
  for the session. The native schema also lacked the `file` and `todo`
  tools, so on a provider where native calling works, plans could not read
  a file or keep a task list; both are in it now. Found by the Metabolism
  engine's decision records.

- **A chat fallback that answered is not a failure.** When planning failed
  and the chat fallback answered, the recorded turn was always `failed`. It
  is now `done` when the fallback replied. The firewall critic's calls are
  counted as their own kind in `/cost`, not as chat.

- **No means the step did not happen.** A step whose confirmation was refused
  is reported as declined, not OK; the plan stops there, and the agentic loop
  ends instead of replanning around the refusal.
- **Exit codes on every turn.** A shell step keeps its exit status outside
  `/agentic` too, without capturing output, so the command keeps its terminal.
- **Daemon:** commands never wait on a pager, and each request starts in the
  daemon's home directory instead of wherever the last `cd` left it.
- **Search results can be read as reported.** `glob` and `grep` under a
  folder report paths with that folder in front, so reading a result works;
  a grep of one file names the file instead of `.`.
- **File lookups that are getting somewhere get more rounds.** A round that
  reads new parts of a file earns another, up to 6 (from 3); searching and
  repeating earn nothing. The last round is told to answer from what it has,
  so a lookup no longer ends with no reply.
- **Reading long files works.** A read returns a window of up to 80 lines,
  says which lines and how long the file is, and takes `start_line` /
  `end_line`. It used to show the planner an arbitrary slice from the middle
  of a long file, which ended in rereading until the budget ran out.

### New

- **Decision records (recording wire v3)**: each recorded turn now says
  where its model calls went and what the planner decided per round, so the
  Metabolism engine can measure which decisions might be compiled into
  cheaper habits (its D-031).

- **`/metabolism`**: opt-in, local-only recording of planner turns for the
  Metabolism engine (`~/.helix/metabolism/`, masked and bounded, wiped by
  `/purge`). harness.md §10.
- **`helix replay`**: plans past requests again with learned lessons; the
  engine's nutrient test drives it over NDJSON. Version 2 replays a turn's
  follow-up rounds too, running only read-only file steps between them, and
  never a command or a change. harness.md §11.
- **`/lessons`**: with recording on, delivers the lessons the engine tested
  into planner turns as fenced data, on a coin flip each turn records, so the
  engine can tell whether a lesson helps. `/lessons forget` stops one at
  once; `/lessons forget --hard` deletes it, here at once and in the
  engine's journal, with an audit record, at the next ingest. Off by default. harness.md §12.

## Helix v1.5.0: Voice & an Agent that can work

v1.0.0 taught the terminal to speak human. v1.5.0 lets you stop typing at it:
full-duplex conversation you can interrupt, a planner that edits files instead
of shelling out to `sed`, and a turn that renders as an instrument rather than
a log.

Existing configs keep working, and typed behaviour is unchanged by design; the
PTY end-to-end suite is the proof.

### What's new

- **Full duplex.** Pick *"Talk over it"* in `/blackbox setup` and the microphone
  stays open for the whole conversation: cut in mid-sentence, and the model
  decides when your turn ended instead of a silence timer. Your own planner
  still does all the reasoning: `gpt-live-1` has no tools and hands every turn
  back, which keeps an external model behind the Instruction Firewall.
- **The agent can work on files.** A `file` tool (`read`, `list`, `glob`,
  `grep`, `edit`, `write`) replaces the old advice to shell out to `sed -i`,
  which exits 0 when its pattern misses and reported edits that never happened.
  Writes are atomic and preserve permissions.
- **The agent shares the plan.** `/todo` is no longer yours alone: the planner
  can open, close and supersede tasks, so a plan written before the work began
  stays honest as the work changes it.
- **Hands-free.** `/blackbox wake on`, on by default, holds the microphone at
  an idle prompt and goes live on any sound. The keyboard always wins a race.
- **A local voice that sounds like one.** Sesame CSM-1B through a Rust sidecar
  (`csm.rs`): no Python, no Docker, no API calls, and the binary stays
  CGO-free.
- **Sight.** An opt-in camera via `ffmpeg`, one downscaled frame at a time, held
  in memory and **never written to disk**, enforced by a filesystem test.
- **A background daemon.** `helix daemon` runs a supervised headless agent with
  its own session memory and wake loop, driven by `helix remote` over NDJSON on
  a 0600 Unix socket (loopback TCP plus a token on Windows).
- **Twelve LLM providers** with circuit-breaker failover, so Helix keeps
  *thinking* when the cloud disappears rather than merely hearing and speaking.
  No model IDs are compiled in any more.
- **Linux edge devices.** A per-board matrix (`docs/edge_deployment.md`) for
  Raspberry Pi 5/4, Jetson, amd64 mini-PCs and arm64 SBCs, plus
  `scripts/edge-setup.sh`.
- **One visual language.** All 57 commands render through the same panels,
  badges and aligned rows, and colour switches itself off when nothing can
  render it.
- **Removing Helix.** `make uninstall` or `helix uninstall` prints a manifest
  and asks before touching anything. If Helix is your login shell it is restored
  **first**, and if that fails the binary is deliberately kept, because the two
  failures together are a machine that cannot open a terminal.

### Security and privacy

- **Voice is an untrusted input channel.** A television or a bystander becomes
  text with user authority the moment it is transcribed, so risk from voice is
  **capped at Medium** whatever the phrasing, and high-risk actions need the
  keyboard.
- **API keys are no longer echoed.** Every key prompt shared the reader that
  asks which provider you want, and that reader echoes, so pasted keys appeared
  in full, in scrollback, and in screenshots.
- **`golang.org/x/net` → v0.56.0** for GO-2026-5942, a panic parsing malformed
  SVCB/HTTPS DNS records. It was reachable from Helix's own full-duplex path
  rather than merely present in the module graph.
- Every archive is Sigstore-signed and ships an SBOM.

### Requirements

- **Building:** Go 1.25+ (Go 1.27+ only if you run the fuzz targets).
- **Full duplex:** libopus (`brew install opus` / `apt install libopus0`). It is
  loaded at runtime, so the release binary stays CGO-free.
- **Listening:** `sox` (preferred) or `ffmpeg`. **Seeing:** `ffmpeg`.
- **Cost:** a full-duplex session bills **$0.05/min while open**, plus whatever
  your planner costs. Nothing else here bills by time.

### Upgrading from v1.0.0

**Wake listening is on for a fresh install and off for an upgrade, and that is
not a bug.** v1.0.0 stored `speech.wake_word.enabled` as a plain `bool`, which
is always written out, so any config it saved holds a literal `false` nobody
chose. The new default fills the key only when it is **absent**, and an explicit
`false` is never overridden, because it is the documented opt-out. Type
`/blackbox wake on` once and it persists. Everything else stays off until you
ask for it.

```text
/blackbox setup     # pick a speech chain, or take the recommended one
/blackbox on        # go live
```

Say **"manual mode"** to return to the keyboard, or **"reboot"** to restart the
shell without losing your place.

### Known limits

- A long spoken sentence is truncated in the live caption rather than wrapped;
  the tail is kept deliberately, because the words just spoken are the useful
  ones.
- The energy-onset wake detector fires on **any** speech or loud sound. True
  phrase spotting needs an openWakeWord-class sidecar; see
  `docs/edge_deployment.md` §5.1.
- Full duplex is refused without a capture binary and libopus; `/blackbox
  status` names whichever is missing.

### Verifying this release

Every archive carries a Sigstore signature (`.sig` + `.pem`) and an SBOM.

```bash
cosign verify-blob \
  --certificate Helix_Linux_x86_64.tar.gz.pem \
  --signature Helix_Linux_x86_64.tar.gz.sig \
  --certificate-identity-regexp "https://github.com/Nibir1/Helix/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  Helix_Linux_x86_64.tar.gz

syft Helix_Linux_x86_64.tar.gz
```

`/reboot` performs the equivalent check on its own: a download is installed only
if its SHA-256 matches the release's checksums file, and the previous binary is
restored automatically if the new one cannot start.

### Installing

Binaries for macOS, Linux and Windows are attached below. Installation,
including the scripted installers and building from source, is documented in
the [README](https://github.com/Nibir1/Helix#quick-start--installation).

---

## Earlier releases

**v1.0.0: The AI-Native Shell.** A single prompt accepting shell commands,
natural language, git workflows and threat-intelligence queries with no mode
switching, behind a multi-layer safety pipeline: Unicode-aware validation, risk
tiering, a directory sandbox and typed confirmations. Kernel-grade confinement
(Seatbelt, bubblewrap, Landlock), an instruction firewall for RAG-augmented
planning, and a signed supply chain.
[Release page](https://github.com/Nibir1/Helix/releases/tag/v1.0.0).

Fuller history lives in the commit log and in `docs/BlackBox_Development.md`,
which records each session's findings rather than only its outcomes.
