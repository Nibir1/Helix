#!/usr/bin/env bash
#
# scripts/release.sh — tag and publish a Helix release.
#
# Run this AFTER merging into main. It does not commit anything: a release tags
# what has already been reviewed and merged, and a script that runs `git add .`
# at release time is a script that ships whatever happened to be lying in the
# working tree.
#
# Everything before the tag is a check. The tag push is the only irreversible
# step, and it comes last, after a confirmation.
#
#   ./scripts/release.sh v1.5.0
#   ./scripts/release.sh v1.5.0 --dry-run     verify only, tag nothing
#   ./scripts/release.sh v1.5.0 --force       replace an existing tag (read the warning)
#   ./scripts/release.sh v1.5.0 --skip-tests  preflight without the full suite
#
set -Eeuo pipefail

# ---------------------------------------------------------------------------
# Output. Colour only when stdout is a terminal, matching what the shell itself
# now does — a release log piped into a file should be plain text.
# ---------------------------------------------------------------------------
if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]]; then
    C_DIM=$'\033[38;5;244m'; C_OK=$'\033[38;5;42m'; C_WARN=$'\033[38;5;214m'
    C_ERR=$'\033[38;5;203m'; C_HDR=$'\033[38;5;45m'; C_OFF=$'\033[0m'
else
    C_DIM=''; C_OK=''; C_WARN=''; C_ERR=''; C_HDR=''; C_OFF=''
fi

step()  { printf '\n%s▸ %s%s\n' "$C_HDR" "$1" "$C_OFF"; }
ok()    { printf '  %s✔%s %s\n' "$C_OK" "$C_OFF" "$1"; }
warn()  { printf '  %s!%s %s\n' "$C_WARN" "$C_OFF" "$1"; }
info()  { printf '  %s%s%s\n' "$C_DIM" "$1" "$C_OFF"; }
die()   { printf '\n  %s✘ %s%s\n\n' "$C_ERR" "$1" "$C_OFF" >&2; exit 1; }

# Repository STATE — which branch, whether the tree is clean, whether main
# matches origin — is a hard stop for a real release and merely informational
# for a dry run. The distinction matters: the point of --dry-run is to find
# problems BEFORE the merge, and a dry run that refuses to leave a feature
# branch can only ever run in the two minutes between merging and tagging,
# which is the moment it is least useful. Code-quality checks (fmt, vet, tests,
# lint, the version constant) still stop a dry run — those are wrong on any
# branch.
state_die() {
    if [[ $DRY_RUN -eq 1 ]]; then
        warn "$1"
        info "(dry run — a real release stops here)"
        STATE_DEFERRED=$((STATE_DEFERRED + 1))
        return 0
    fi
    die "$1"
}

# A failure anywhere should say WHERE, not just stop. `set -e` on its own leaves
# you guessing which of forty commands exited non-zero.
trap 'die "failed at line $LINENO: ${BASH_COMMAND}"' ERR

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------
TAG=""
DERIVED_TAG=0
DRY_RUN=0
FORCE=0
SKIP_TESTS=0
STATE_DEFERRED=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)    DRY_RUN=1 ;;
        --force)      FORCE=1 ;;
        --skip-tests) SKIP_TESTS=1 ;;
        -h|--help)
            sed -n '3,20p' "$0" | sed 's|^# \{0,1\}||'
            exit 0 ;;
        -*)           die "unknown option: $1" ;;
        *)
            # Anything that is not a flag is the tag attempt. Taken here rather
            # than matched as `v*` so `1.5.0` reaches the format check below and
            # is told what is wrong with it, instead of "unknown argument".
            [[ -z "$TAG" ]] || die "more than one tag given: $TAG and $1"
            TAG="$1" ;;
    esac
    shift
done

# The tag defaults to the version the code already declares.
#
# Not a convenience: the script verifies that HelixVersion matches the tag
# anyway, so deriving one from the other makes the two impossible to disagree
# about and leaves exactly one place to edit when the version changes. It also
# lets `make release` work without arguments, which is how it is wired.
if [[ -z "$TAG" ]]; then
    DECLARED="$(grep -oE 'HelixVersion[[:space:]]*=[[:space:]]*"[^"]+"' \
        "$(git rev-parse --show-toplevel 2>/dev/null || echo .)/internal/config/config.go" 2>/dev/null \
        | head -1 | grep -oE '"[^"]+"' | tr -d '"' || true)"
    [[ -n "$DECLARED" ]] || die "no tag given and HelixVersion could not be read.
    usage: ./scripts/release.sh [vX.Y.Z] [--dry-run] [--force] [--skip-tests]"
    TAG="v$DECLARED"
    DERIVED_TAG=1
fi

# The tag must be a version goreleaser and the self-updater can both parse.
# internal/update rejects anything it cannot read rather than treating it as
# 0.0.0, so a malformed tag produces a release nobody can update to.
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
    || die "tag must look like v1.5.0 or v1.5.0-rc.1, got: $TAG"

VERSION="${TAG#v}"

# Locate the repository by ASKING GIT, not by the script's own path.
#
# BASH_SOURCE is the obvious way and it is wrong under `bash <(...)`, a pipe, or
# a symlink into another tree: it resolves to /dev/fd/63 and the script cds to
# /dev, where every subsequent git command fails with a confusing error about
# filesystem boundaries. Preferring the script's directory when it IS a real
# checkout keeps `./scripts/release.sh` working from anywhere.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
REPO_ROOT=""
if [[ -n "$SCRIPT_DIR" ]]; then
    REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || true)"
fi
if [[ -z "$REPO_ROOT" ]]; then
    REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
fi
[[ -n "$REPO_ROOT" ]] || die "not inside a git repository — run this from the Helix checkout"
cd "$REPO_ROOT"

# And prove it is the right repository, so a stray copy of this script cannot
# tag something else entirely.
[[ -f "internal/config/config.go" && -f ".goreleaser.yml" ]] \
    || die "$REPO_ROOT does not look like the Helix repository"

printf '\n%s⚡ Helix %s release%s\n' "$C_HDR" "$TAG" "$C_OFF"
info "$REPO_ROOT"
[[ $DERIVED_TAG -eq 1 ]] && info "tag taken from HelixVersion in internal/config/config.go"
[[ $DRY_RUN -eq 1 ]] && warn "dry run — nothing will be tagged or pushed"

# ---------------------------------------------------------------------------
# 1. Tooling
# ---------------------------------------------------------------------------
step "tooling"
for tool in git go make; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done
ok "git, go, make"

HAVE_GH=0
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    HAVE_GH=1
    ok "gh authenticated — CI and the published release will be checked"
else
    warn "gh not available; skipping the CI watch and the post-release check"
    info "the release still publishes — verify it by hand at the URL printed below"
fi

# ---------------------------------------------------------------------------
# 2. Repository state
#
# Every check here is a reason NOT to release, and each has a specific failure
# behind it: releasing from a feature branch, releasing uncommitted work,
# releasing something main does not have.
# ---------------------------------------------------------------------------
step "repository"

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$BRANCH" == "main" ]]; then
    ok "on main"
else
    state_die "on branch '$BRANCH' — release from main, after the merge"
fi

if [[ -n "$(git status --porcelain)" ]]; then
    git status --short | sed 's/^/      /'
    state_die "working tree is not clean — a release tags what is already committed"
else
    ok "working tree clean"
fi

git fetch --quiet origin --tags
LOCAL="$(git rev-parse @)"
REMOTE="$(git rev-parse '@{u}' 2>/dev/null || echo "")"
if [[ -z "$REMOTE" ]]; then
    state_die "$BRANCH has no upstream — push it first"
elif [[ "$LOCAL" != "$REMOTE" ]]; then
    BASE="$(git merge-base @ '@{u}')"
    if [[ "$LOCAL" == "$BASE" ]]; then
        state_die "$BRANCH is behind origin — pull first"
    elif [[ "$REMOTE" == "$BASE" ]]; then
        state_die "$BRANCH has unpushed commits — push them, let CI pass, then release"
    else
        state_die "$BRANCH and origin have diverged — resolve that before releasing"
    fi
else
    ok "$BRANCH matches origin ($(git rev-parse --short HEAD))"
fi

# ---------------------------------------------------------------------------
# 3. The tag
#
# Re-tagging a released version is genuinely dangerous NOW in a way it was not
# before v1.5.0: the self-updater verifies a download against the checksums file
# published with that release. Replacing the artifacts under a tag someone has
# already fetched means their update refuses with a checksum mismatch — which
# looks exactly like an attack. Hence --force rather than delete-and-recreate by
# default, which is what the old script did.
# ---------------------------------------------------------------------------
step "tag"

TAG_EXISTS_LOCAL=0;  git rev-parse -q --verify "refs/tags/$TAG" >/dev/null && TAG_EXISTS_LOCAL=1
TAG_EXISTS_REMOTE=0; git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1 && TAG_EXISTS_REMOTE=1

if [[ $TAG_EXISTS_REMOTE -eq 1 && $FORCE -eq 0 ]]; then
    state_die "$TAG is already published. If artifacts exist, anyone who fetched its
    checksums file will see a MISMATCH after a re-tag — which is what a tampered
    download looks like. Prefer a new patch version. To replace it anyway:
    ./scripts/release.sh $TAG --force"
fi
if [[ $TAG_EXISTS_REMOTE -eq 1 ]]; then
    warn "$TAG exists on origin and will be REPLACED"
    warn "anyone who already fetched it will see different artifacts under the same name"
fi
[[ $TAG_EXISTS_LOCAL -eq 1 ]] && info "a local $TAG exists and will be replaced"
[[ $TAG_EXISTS_REMOTE -eq 0 && $TAG_EXISTS_LOCAL -eq 0 ]] && ok "$TAG is unused"

# ---------------------------------------------------------------------------
# 4. Version consistency
#
# The most valuable check in this script, and the one the old one lacked.
#
# internal/config.HelixVersion is overridden by goreleaser's ldflags for the
# published binaries — but NOT for `make current`, `go install`, or a plain
# `go build`. Its own doc comment says the constant "has to track the tag or
# /version lies about which Helix you are running". A mismatch ships a source
# build that misreports itself, and the self-updater compares against exactly
# this constant: set it too high and Helix never sees the release as newer.
# ---------------------------------------------------------------------------
step "version"

CONST_FILE="internal/config/config.go"
CODE_VERSION="$(grep -oE 'HelixVersion[[:space:]]*=[[:space:]]*"[^"]+"' "$CONST_FILE" \
    | head -1 | grep -oE '"[^"]+"' | tr -d '"')"
[[ -n "$CODE_VERSION" ]] || die "could not read HelixVersion from $CONST_FILE"

if [[ "$CODE_VERSION" != "$VERSION" ]]; then
    die "HelixVersion is \"$CODE_VERSION\" but the tag is $TAG.
    A source build would report the wrong version, and the self-updater compares
    the running version against this constant. Fix it in $CONST_FILE, commit,
    push, then re-run:
      HelixVersion  = \"$VERSION\""
fi
ok "HelixVersion = $CODE_VERSION matches $TAG"

# goreleaser embeds docs/RELEASE_NOTES.md VERBATIM as the release body
# (`release.header: {{ readFile ... }}`). Notes that never mentioned this
# version publish a release page describing a different one.
if ! grep -qiE "v?${VERSION//./\\.}" docs/RELEASE_NOTES.md; then
    warn "docs/RELEASE_NOTES.md never mentions $VERSION"
    warn "goreleaser publishes that file verbatim as the release body"
fi
ok "release notes present ($(wc -l < docs/RELEASE_NOTES.md | tr -d ' ') lines)"

# ---------------------------------------------------------------------------
# 5. The build gate
#
# Cross-compilation is checked because the release builds six targets and a
# platform-specific break (a _unix.go without its _windows.go sibling) does not
# show up in a local build until the pipeline has already started.
# ---------------------------------------------------------------------------
step "build"

gofmt -l . >/tmp/helix-release-gofmt.txt 2>/dev/null || true
if [[ -s /tmp/helix-release-gofmt.txt ]]; then
    sed 's/^/      /' /tmp/helix-release-gofmt.txt
    die "gofmt would reformat the files above"
fi
ok "gofmt clean"

go build ./... && ok "builds"
go vet ./... && ok "vet clean"

for target in "darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64" "windows/amd64"; do
    GOOS="${target%/*}" GOARCH="${target#*/}" go build -o /dev/null ./cmd/helix
done
ok "cross-compiles for all five release targets"

if [[ $SKIP_TESTS -eq 1 ]]; then
    warn "tests skipped (--skip-tests) — CI will still run them"
else
    step "tests"
    info "unit + integration"
    go test ./... -count=1 >/tmp/helix-release-test.txt 2>&1 \
        || { tail -30 /tmp/helix-release-test.txt | sed 's/^/      /'; die "tests failed"; }
    ok "$(grep -c '^ok' /tmp/helix-release-test.txt) packages pass"

    info "end-to-end (real binary, PTY harness)"
    go test ./tests/e2e/... -count=1 -timeout 600s >/tmp/helix-release-e2e.txt 2>&1 \
        || { tail -30 /tmp/helix-release-e2e.txt | sed 's/^/      /'; die "e2e failed"; }
    ok "e2e passes"

    info "lint"
    make lint >/tmp/helix-release-lint.txt 2>&1 \
        || { tail -20 /tmp/helix-release-lint.txt | sed 's/^/      /'; die "lint failed"; }
    ok "lint clean"

    if command -v govulncheck >/dev/null 2>&1; then
        if govulncheck ./... >/tmp/helix-release-vuln.txt 2>&1; then
            ok "no known vulnerabilities"
        else
            warn "govulncheck reported findings — review /tmp/helix-release-vuln.txt"
            warn "this does not block the release; decide deliberately"
        fi
    else
        info "govulncheck not installed; skipping (make sec-scan installs it)"
    fi
fi

# ---------------------------------------------------------------------------
# 6. Confirm
#
# The push below is public and hard to undo. Everything above this line is
# reversible; nothing below it is.
# ---------------------------------------------------------------------------
if [[ $DRY_RUN -eq 1 ]]; then
    step "dry run complete"
    if [[ $STATE_DEFERRED -gt 0 ]]; then
        # Say this plainly. "Every check passed" after deferring three of them
        # is the kind of summary that gets trusted once and regretted later.
        warn "code checks passed; $STATE_DEFERRED repository-state check(s) deferred above"
        info "those are hard stops for a real release — re-run from main once merged"
    else
        ok "every check passed — re-run without --dry-run to publish $TAG"
    fi
    exit 0
fi

step "ready to publish"
info "tag:     $TAG"
info "commit:  $(git rev-parse --short HEAD)  $(git log -1 --pretty=%s | cut -c1-60)"
info "trigger: .github/workflows/release.yml → goreleaser (6 binaries, SBOMs, cosign)"
printf '\n  %sPush %s? This is public and hard to undo.%s [y/N] ' "$C_WARN" "$TAG" "$C_OFF"
read -r REPLY </dev/tty
[[ "$REPLY" =~ ^[Yy]$ ]] || die "cancelled — nothing was pushed"

# ---------------------------------------------------------------------------
# 7. Tag and push
# ---------------------------------------------------------------------------
step "publishing"

if [[ $TAG_EXISTS_LOCAL -eq 1 ]]; then
    git tag -d "$TAG" >/dev/null
fi
if [[ $TAG_EXISTS_REMOTE -eq 1 ]]; then
    git push --delete origin "$TAG" >/dev/null
    warn "deleted the previously published $TAG"
fi

git tag -a "$TAG" -m "Helix $TAG"
ok "tagged $(git rev-parse --short "$TAG")"

git push origin "$TAG"
ok "pushed — the release workflow is starting"

REPO_SLUG="$(git remote get-url origin \
    | sed -E 's#^.*[:/]([^/]+/[^/]+?)(\.git)?$#\1#')"
RELEASE_URL="https://github.com/${REPO_SLUG}/releases/tag/${TAG}"

# ---------------------------------------------------------------------------
# 8. Watch, then verify the release is actually installable
#
# The second half is new in v1.5.0 and is the part nobody would think to do.
# /reboot self-updates by matching a per-platform asset AND reading the SHA-256
# out of the release's checksums file — and it REFUSES rather than degrading if
# either is missing. A release that builds fine but publishes no checksums file
# is a release no existing Helix can update to, and you would only find that out
# from a user.
# ---------------------------------------------------------------------------
if [[ $HAVE_GH -eq 1 ]]; then
    step "release workflow"
    info "watching (Ctrl+C is safe — the workflow keeps running)"
    sleep 10
    RUN_ID="$(gh run list --workflow=release.yml --limit 1 --json databaseId \
        --jq '.[0].databaseId' 2>/dev/null || echo "")"
    if [[ -n "$RUN_ID" ]]; then
        gh run watch "$RUN_ID" --exit-status \
            && ok "workflow succeeded" \
            || warn "workflow failed or was cancelled — see: gh run view $RUN_ID --log-failed"
    else
        warn "could not find the workflow run; check the Actions tab"
    fi

    step "self-update readiness"
    ASSETS="$(gh release view "$TAG" --json assets --jq '.assets[].name' 2>/dev/null || echo "")"
    if [[ -z "$ASSETS" ]]; then
        warn "no assets published yet — re-check once the workflow finishes:"
        info "gh release view $TAG --json assets --jq '.assets[].name'"
    else
        if grep -qiE 'checksums.*\.txt$' <<<"$ASSETS"; then
            ok "checksums file published — /reboot can verify a download"
        else
            warn "NO checksums file in the release."
            warn "Every self-update will REFUSE: internal/update treats a missing"
            warn "manifest as uninstallable rather than downgrading to no check."
        fi
        MISSING=""
        for want in "Darwin_arm64" "Darwin_x86_64" "Linux_x86_64" "Linux_arm64" "Windows_x86_64"; do
            grep -q "$want" <<<"$ASSETS" || MISSING="$MISSING $want"
        done
        if [[ -z "$MISSING" ]]; then
            ok "all five platform archives present"
        else
            warn "no archive for:$MISSING — those platforms cannot self-update"
        fi
    fi
fi

step "done"
ok "Helix $TAG"
info "$RELEASE_URL"
printf '\n  %sVerify the update path from an older build:%s\n' "$C_DIM" "$C_OFF"
info "  /reboot check     reports the new version without installing"
info "  /reboot           installs it and restarts"
printf '\n'
