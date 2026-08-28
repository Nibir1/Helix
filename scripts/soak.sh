#!/usr/bin/env bash
# scripts/soak.sh
# Purpose: BlackBox Phase 4 (P4.14) daemon soak — run `helix daemon` under a
# minimal supervisor, poll `helix remote status` on a cadence, and append
# uptime metrics to a JSONL log. Optionally kill -9 the daemon on an interval
# to exercise crash-restart recovery and stale-socket reclamation. No AI, no
# mic — status probes only.
#
# Environment:
#   SOAK_DURATION  seconds to run (default 259200 = 72h)
#   SOAK_INTERVAL  seconds between status polls (default 60)
#   SOAK_KILL      seconds between kill -9 recovery drills; 0 = never (default)
#   SOAK_HOME      isolated HOME for the daemon (default: fresh mktemp dir)
#   SOAK_DIST      build output directory (default: <repo>/dist)
#
# Usage:
#   SOAK_DURATION=300 SOAK_INTERVAL=10 SOAK_KILL=60 ./scripts/soak.sh
set -u

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${SOAK_DIST:-$ROOT_DIR/dist}"
BIN="$DIST_DIR/helix"

SOAK_DURATION="${SOAK_DURATION:-259200}"
SOAK_INTERVAL="${SOAK_INTERVAL:-60}"
SOAK_KILL="${SOAK_KILL:-0}"
SOAK_HOME="${SOAK_HOME:-$(mktemp -d "${TMPDIR:-/tmp}/helix-soak.XXXXXX")}"
METRICS="$SOAK_HOME/metrics.jsonl"

mkdir -p "$SOAK_HOME"
echo "Soak home:    $SOAK_HOME"
echo "Duration:     ${SOAK_DURATION}s"
echo "Poll interval: ${SOAK_INTERVAL}s"
echo "Kill cadence: ${SOAK_KILL}s (0 = never)"

# --- Build the binary once ---
echo "Building helix..."
mkdir -p "$DIST_DIR"
( cd "$ROOT_DIR" && go build -o "$BIN" ./cmd/helix ) || { echo "build failed"; exit 1; }

start_daemon() {
  HOME="$SOAK_HOME" "$BIN" daemon >>"$SOAK_HOME/daemon.log" 2>&1 &
  DAEMON_PID=$!
}

probe_uptime() {
  # Emits the daemon-reported uptime_s, or 0 when the IPC probe fails.
  local out
  out=$(HOME="$SOAK_HOME" "$BIN" remote status 2>/dev/null) || { echo 0; return; }
  printf '%s' "$out" | awk '/^uptime_s/{print $2}'
}

# log_event appends one NDJSON line: the timestamp goes INSIDE the object.
#
# It used to print "<timestamp> <json>", which made every line of a file named
# metrics.jsonl unparseable as JSON — so the acceptance criterion this script
# exists to evidence ("99.5% uptime over 72h, metrics file evidences it") could
# only be checked by eye across thousands of lines. The ts field name matches
# internal/metrics so the same reader handles both.
log_event() {
  printf '{"ts":"%s",%s\n' "$(date -u +%FT%TZ)" "${1#\{}" | tee -a "$METRICS"
}

start_daemon
restarts=0
now=$(date +%s)
deadline=$(( now + SOAK_DURATION ))
next_kill=$(( SOAK_KILL > 0 ? now + SOAK_KILL : 0 ))

while [ "$(date +%s)" -lt "$deadline" ]; do
  sleep "$SOAK_INTERVAL"

  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    restarts=$(( restarts + 1 ))
    log_event "{\"event\":\"restart\",\"n\":$restarts}"
    start_daemon
    now=$(date +%s)
    next_kill=$(( SOAK_KILL > 0 ? now + SOAK_KILL : 0 ))
    continue
  fi

  up="$(probe_uptime)"
  log_event "{\"uptime_s\":${up:-0},\"restarts\":$restarts}"

  if [ "$SOAK_KILL" -gt 0 ] && [ "$(date +%s)" -ge "$next_kill" ]; then
    log_event "{\"event\":\"kill_drill\"}"
    kill -9 "$DAEMON_PID" 2>/dev/null || true
    now=$(date +%s)
    next_kill=$(( now + SOAK_KILL ))
  fi
done

kill "$DAEMON_PID" 2>/dev/null || true
echo "Soak complete. Metrics: $METRICS"
echo "Daemon log: $SOAK_HOME/daemon.log"
echo
echo "The daemon also wrote its own liveness heartbeats. For the availability"
echo "verdict against the 99.5% target, read them with Helix itself:"
echo "  HOME=\"$SOAK_HOME\" $BIN"
echo "  /blackbox stats"
