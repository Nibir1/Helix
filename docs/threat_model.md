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
2. **Sanitization** — invisible/bidi Unicode, markdown fences, backticks, JSON
   braces, and imperative patterns ("ignore previous instructions", "you must",
   "run curl …", pipe-into-shell) are stripped or filtered.
3. **Canary honeypot** — a per-request random token embedded in the data block;
   its appearance in model output aborts with an injection alert.
4. **Critic pass (risk-gated)** — a low-temperature, strict-JSON call seeing
   only the user request + proposed commands; triggered exclusively by
   unsolicited external URLs; "no"/garbage/unreachable all quarantine.
5. **Provenance escalation** — any plan command carrying a URL/host/path token
   present in retrieved context but absent from user input is forced to
   Medium risk (mandatory confirmation).

## Residual Risk (honest statement)
Distinguishing instruction from data in natural language is undecidable in the
general case; a sufficiently subtle injection can still pass all five controls.
The firewall therefore does not claim to "solve" prompt injection — it makes a
successful attack require defeating five independent layers, and limits blast
radius via the existing safety pipeline (validation, risk tiers, sandbox,
typed confirmations).