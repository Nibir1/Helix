#!/usr/bin/env bash
#
# scripts/check-workflows.sh — static checks for .github/workflows.
#
# Two checks, because one of them is not enough and finding that out was the
# whole reason this file exists.
#
#   1. actionlint. Expression syntax, unknown action inputs, deprecated
#      constructs, and shellcheck over `run:` blocks. Broad and worth having.
#
#   2. A shell-declaration check that actionlint does NOT perform.
#
# The second exists because of a specific failure. A retry loop written in bash
# was added to a job whose matrix is [ubuntu, macos, windows]. On Windows the
# default shell is PowerShell, which cannot parse `for x in 1 2 3; do`, and
# every Windows run failed with "Missing opening '(' after keyword 'for'".
#
# actionlint was then tested against that exact workflow and exited 0. It is not
# a gap in the tool so much as a limit of static analysis: `runs-on` is
# `${{ matrix.os }}`, a value that does not exist until the job runs, so
# nothing can statically know which shell a step will get. actionlint assumes
# bash and shellchecks it as bash — which is right for two legs of the matrix
# and wrong for the third.
#
# So the rule is enforced structurally instead: if a job CAN run on Windows,
# every multi-line `run:` in it must say which shell it is written for. A single
# short command is exempt because it is the case that genuinely works in both.
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

if [[ ! -d .github/workflows ]]; then
    echo "no .github/workflows directory — nothing to check"
    exit 0
fi

fail=0

# --- 1. actionlint ---------------------------------------------------------
# Found on PATH, or where `go install` put it.
#
# "not installed" was reported on a machine where the binary existed in
# ~/go/bin the whole time — a tool installed by `go install` is not
# necessarily on PATH, and reporting it absent sends the user to install
# something they already have. Same conclusion the Makefile reached for
# govulncheck and findHuggingFaceCLI reached for the HF CLI.
gotoolbin="$(go env GOBIN 2>/dev/null || true)"
[[ -n "$gotoolbin" ]] || gotoolbin="$(go env GOPATH 2>/dev/null || true)/bin"

actionlint_bin="$(command -v actionlint 2>/dev/null || true)"
if [[ -z "$actionlint_bin" && -x "$gotoolbin/actionlint" ]]; then
    actionlint_bin="$gotoolbin/actionlint"
fi

if [[ -n "$actionlint_bin" ]]; then
    if "$actionlint_bin"; then
        echo "actionlint: clean"
    else
        fail=1
    fi
else
    echo "actionlint not installed; skipping (CI runs it)"
    echo "  go install github.com/rhysd/actionlint/cmd/actionlint@latest"
    echo "  (looked on PATH and in $gotoolbin)"
fi

# --- 2. The shell every Windows-capable step is written for ----------------
python3 - <<'PY' || fail=1
import glob
import sys

try:
    import yaml
except ImportError:
    print("PyYAML unavailable; skipping the shell-declaration check")
    sys.exit(0)

problems = []
for path in sorted(glob.glob(".github/workflows/*.yml") + glob.glob(".github/workflows/*.yaml")):
    doc = yaml.safe_load(open(path)) or {}
    for job_name, job in (doc.get("jobs") or {}).items():
        matrix_os = ((job.get("strategy") or {}).get("matrix") or {}).get("os")
        targets = matrix_os if matrix_os else [job.get("runs-on")]
        if not any("windows" in str(t) for t in targets):
            continue  # this job can never get a PowerShell default
        # A job-level default counts; the rule is that SOMETHING says it.
        job_default = ((job.get("defaults") or {}).get("run") or {}).get("shell")
        for step in job.get("steps") or []:
            run = step.get("run")
            if not run or "\n" not in run.strip():
                continue  # a single command is the case that works in both
            if step.get("shell") or job_default:
                continue
            problems.append(
                f"  {path}\n"
                f"    job:  {job_name}  (matrix includes Windows)\n"
                f"    step: {step.get('name') or '(unnamed)'}\n"
                f"    a multi-line `run:` with no `shell:`. On Windows the default\n"
                f"    is PowerShell, which will not parse bash. Add `shell: bash`."
            )

if problems:
    print("workflow steps that do not say which shell they are written for:\n")
    print("\n\n".join(problems))
    sys.exit(1)
print("shell declarations: every Windows-capable multi-line step names its shell")
PY

# --- 3. Toolchain pins that must agree -------------------------------------
#
# Three files name the golangci-lint version and four name the Go version, and
# they are not independent choices. golangci-lint carries a type-checker built
# against a specific Go release and cannot read export data from a newer one, so
# a Go bump without a linter bump fails every job before a line of this
# repository is type-checked — with errors naming the STANDARD LIBRARY inside
# the toolchain cache rather than any file here. That has now happened twice
# (v1.59.1 against Go 1.25, v2.5.0 against Go 1.27), which is twice more than a
# version string copied into three files deserves.
python3 - <<'PIN' || fail=1
import glob
import re
import sys

lint_pin = re.compile(r"golangci-lint@(v[0-9]+\.[0-9]+\.[0-9]+)")

found = {}
for path in [".github/workflows/ci.yml", "Makefile", ".golangci.yml"]:
    try:
        text = open(path).read()
    except OSError:
        continue
    versions = set(lint_pin.findall(text))
    if versions:
        found[path] = versions

if not found:
    print("no golangci-lint pin found to check")
else:
    everything = set().union(*found.values())
    if len(everything) != 1:
        print("golangci-lint pins disagree:\n")
        for path, versions in sorted(found.items()):
            print("  %s: %s" % (path, ", ".join(sorted(versions))))
        print("\nAll three must name one version: CI installs it, the Makefile hint")
        print("tells a developer what to install, and .golangci.yml declares what")
        print("the config was verified against.")
        sys.exit(1)
    print("golangci-lint pin: %s agrees across %d files" % (everything.pop(), len(found)))

go_pin = re.compile(r"go-version:\s*\[?'([0-9]+\.[0-9]+)'\]?")
go_found = {}
for path in sorted(glob.glob(".github/workflows/*.yml")):
    versions = set(go_pin.findall(open(path).read()))
    if versions:
        go_found[path] = versions

if go_found:
    everything = set().union(*go_found.values())
    if len(everything) != 1:
        print("\nGo version pins disagree across workflows:\n")
        for path, versions in sorted(go_found.items()):
            print("  %s: %s" % (path, ", ".join(sorted(versions))))
        print("\nThe build that ships, the suite that tests it and the scan that")
        print("audits it should agree on the compiler. See SECURITY.md.")
        sys.exit(1)
    print("go-version pin: %s agrees across %d workflows" % (everything.pop(), len(go_found)))
PIN

exit $fail
