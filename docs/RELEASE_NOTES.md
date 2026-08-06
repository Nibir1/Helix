## Helix v1.0.0 — The AI-Native Shell

Helix inverts the terminal paradigm: instead of forcing humans to speak
machine, the machine learns to speak human. A single prompt accepts raw shell
commands, natural-language requests, git workflows, package operations, and
defensive threat-intelligence queries — with zero mode switching. Every action
flows through a multi-layer safety pipeline (Unicode-aware validation → risk
tiering → directory sandbox → typed confirmations), delivering power without
recklessness.

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

### Install

```bash
git clone https://github.com/Nibir1/Helix.git && cd Helix && ./scripts/install.sh