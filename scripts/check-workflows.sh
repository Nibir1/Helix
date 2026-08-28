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
if command -v actionlint >/dev/null 2>&1; then
    if actionlint; then
        echo "actionlint: clean"
    else
        fail=1
    fi
else
    echo "actionlint not installed; skipping (CI runs it)"
    echo "  go install github.com/rhysd/actionlint/cmd/actionlint@latest"
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

exit $fail
