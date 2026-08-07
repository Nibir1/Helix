## Helix v1.0.0 — The AI-Native Shell

Helix inverts the terminal paradigm: instead of forcing humans to speak
machine, the machine learns to speak human. A single prompt accepts raw shell
commands, natural-language requests, git workflows, package operations, and
defensive threat-intelligence queries with zero mode switching. Every action
flows through a multi-layer safety pipeline (Unicode-aware validation → risk
tiering → directory sandbox → typed confirmations), delivering power without
recklessness.

Helix is knowledge sovereign. Hundreds of thousands of CVEs, tens of thousands 
of exploit references, the full CISA KEV catalog, MITRE ATT&CK, and ~479 MAN pages 
living as ~1,000 vector documents — all in a SQLite file on your disk, searchable 
offline, synced with checkpointed, rate-limit-respecting patience. 
No cloud. No telemetry. Keys in a 0600 file. 
In an age when intelligence is treated as a subscription, Helix has made it a possession.

### Highlights

- **Unified input classification** — `ls -la`, `why is my build failing?`,
  and `/vuln CVE-2024-1234` coexist in one prompt.
- **Multi-provider AI** — OpenAI, Anthropic, DeepSeek, Kimi, Qwen, GLM,
  Ollama, llama.cpp, and custom OpenAI-compatible endpoints, driven by a
  strict-JSON planner with truncation-resistant parsing.
- **Local RAG + live threat intel** — 900+ indexed MAN pages plus NVD, CISA
  KEV, Exploit-DB, and MITRE ATT&CK in a SQLite/FTS5 knowledge base.
- **Safety-first execution** — hard blocks for `rm -rf /`, `curl | sh`, and
  `eval`; confirmations for medium risk; critical-package protection.
- **Authorized recon** — `/scan` requires written scope; dangerous flags are
  blocked by default.
- **Helix UX** — TrueColor animated prompt, live syntax highlighting,
  in-place resize healing, and synthetic tonal audio synced to the typewriter.

---
## Quick Start & Installation

### Option 1: Automated Installer (macOS / Linux)
The fastest way to get Helix running. This one-liner clones the repository, builds the optimized binary, initializes your `~/.helix` configuration directories, and prompts you to set Helix as your default system shell.

```bash
git clone https://github.com/Nibir1/Helix.git && cd Helix && ./scripts/install.sh
```

### Option 2: Windows (PowerShell)
Open PowerShell as Administrator and run the automated Windows setup script. This will build the binary, add it to your system `PATH`, and optionally bootstrap Ollama.

```powershell
git clone https://github.com/Nibir1/Helix.git; cd Helix; .\scripts\install.ps1
```

### Option 3: Go Install (Cross-Platform)
If you already have Go 1.25+ installed and just want the binary in your `$GOPATH/bin`:

```bash
go install github.com/Nibir1/Helix/cmd/helix@latest
```

### Option 4: Pre-compiled Binaries (No Build Required)
Don't want to build from source? Download the latest pre-compiled binary, checksums, and archives for your OS directly from this **Releases Page**.

---

