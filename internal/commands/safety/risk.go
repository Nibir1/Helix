// internal/commands/safety/risk.go
package safety

import (
	"regexp"
	"strings"
)

type ShellRiskLevel int

const (
	ShellRiskLow ShellRiskLevel = iota
	ShellRiskMedium
	ShellRiskHigh
)

// The risk patterns, compiled ONCE at package load.
//
// They were `regexp.MustCompile(...)` expressions written inline at their use
// sites, which recompiles the pattern on every call — and these functions sit on
// the hot path of the safety pipeline, so "every call" is every command Helix
// considers, typed or spoken. Compilation dominated: classifying five ordinary
// commands cost 53µs and **995 allocations**, of which 199 allocations per
// command were the regex compiler running over constant strings. It is now 15µs
// and 5 allocations — see hotpath_bench_test.go, which holds both numbers.
//
// Found while investigating a red fuzz run, NOT by it: that failure was a
// toolchain race (see the note in .github/workflows/ci.yml) and this code was
// not its cause. Worth stating plainly, because the tempting version of this
// comment — "the fuzzer was slow because of the compiles" — is one I wrote and
// then could not support. Measured before and after with the same target and
// budget: 139k executions before the hoist, 110k after. Fuzz throughput here is
// dominated by the mutator and the growing corpus, not by this function, so the
// only honest claim is the benchmark's.
//
// Hoisting is behaviour-preserving by construction: the pattern strings are
// moved verbatim and evaluated in the same order. A differential test
// (TestHoistedPatternsMatchInlineOriginals) says so by experiment as well.
var (
	rmRfBroadRe    = regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`)
	sudoShellRe    = regexp.MustCompile(`(?i)sudo\s+(?:/[a-z0-9_./-]+/)?(bash|sh|zsh|dash|ksh|ash|fish)\b`)
	shellFromTmpRe = regexp.MustCompile(`(?i)(bash|sh|zsh|dash|ksh|ash|fish)\s+(/tmp/|/var/tmp/|/dev/shm/)`)

	// redirectToFileRe finds a redirection that writes somewhere.
	//
	// This replaces `strings.Contains(lc, " > ") || strings.Contains(lc, ">>")`,
	// which required spaces on BOTH sides of a single `>` and therefore missed
	// every redirect written without them. Measured, because it is hard to
	// believe otherwise:
	//
	//	echo x > file.txt                 MEDIUM
	//	echo x >file.txt                  LOW      ← same command, one space
	//	echo key >~/.ssh/authorized_keys  LOW
	//	cat a 2>err.log                   LOW
	//
	// A silent overwrite of authorized_keys classified as LOW is a missing
	// confirmation, not a cosmetic misgrade — LOW is the tier that does not ask.
	//
	// The shape: an operator not preceded by `-`, `=`, `<` or `>` (so `->` and
	// `=>` in a grep pattern or an awk script are not redirects), one or two
	// `>`, then optional space, then something that starts like a path. The
	// path-ish requirement is what keeps `2>&1` out — a descriptor dup writes no
	// file — and stops `echo '<html>'` reading as a redirect into `'`.
	//
	// It over-triggers on text that merely CONTAINS an arrow into a word, e.g.
	// `echo "a>b"`, and that direction is deliberate: the cost is one
	// confirmation prompt with a wrong reason, against a silent write.
	redirectToFileRe = regexp.MustCompile(`(?:^|[^-=<>])>{1,2}\s*[a-z0-9_./~$-]`)
)

// AnalyzeShellRisk classifies a *validated* command into LOW / MEDIUM / HIGH risk,
// and returns human-readable reasons for MEDIUM/HIGH used by the Agent UX.
//
// IMPORTANT:
//   - This is a *soft* layer: ValidateAndCleanShellCommand is the hard blocker.
//   - HIGH risk commands should generally already be rejected earlier; this is mostly
//     for UX / explanation and future extensions.
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	lc := strings.ToLower(strings.TrimSpace(cmd))
	var reasons []string

	// Obvious dangerous patterns → HIGH
	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {
		reasons = append(reasons, "formats filesystems (mkfs)")
	}

	if rmRfBroadRe.MatchString(lc) {
		reasons = append(reasons, "removes almost everything with 'rm -rf'")
	}

	// FIX (helm-incident): share the hardened regex from shell.go so
	// "| sudo bash", "| env sh", "| /bin/bash", etc. are correctly
	// classified as HIGH risk instead of slipping through as LOW risk.
	if pipeIntoShellRe.MatchString(lc) {
		reasons = append(reasons, "pipes output directly into a shell (possibly via sudo/env)")
	}

	// FIX (AI Workaround Loophole): Catch sudo bash/sh and /tmp/ execution.
	if sudoShellRe.MatchString(lc) {
		reasons = append(reasons, "executes a shell interpreter with sudo (e.g., 'sudo bash')")
	}
	if shellFromTmpRe.MatchString(lc) {
		reasons = append(reasons, "executes a script from a temporary directory")
	}

	if strings.Contains(lc, "eval ") {
		reasons = append(reasons, "uses 'eval' to execute dynamic shell code")
	}

	if len(reasons) > 0 {
		return ShellRiskHigh, reasons
	}

	// Medium-risk patterns: modifying files / permissions / config
	med := []string{}

	if strings.Contains(lc, "sed ") && (strings.Contains(lc, " -i") || strings.Contains(lc, " -i''") || strings.Contains(lc, " -i ''")) {
		med = append(med, "edits files in-place with sed -i")
	}

	if strings.Contains(lc, "chmod ") {
		med = append(med, "changes file permissions (chmod)")
	}

	if strings.Contains(lc, "chown ") {
		med = append(med, "changes file ownership (chown)")
	}

	if redirectToFileRe.MatchString(lc) {
		med = append(med, "writes or appends to files using redirection")
	}

	if len(med) > 0 {
		return ShellRiskMedium, med
	}

	// Everything else is treated as low risk (after hard validation).
	return ShellRiskLow, nil
}
