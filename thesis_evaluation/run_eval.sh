#!/bin/bash

# ==============================================================================
# Helix Thesis Evaluation Harness - FINAL 50-TASK RUNNER
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

DATASET_FILE="$SCRIPT_DIR/dataset.csv"
OUTPUT_DIR="$SCRIPT_DIR/telemetry_results"
LINUX_DATA_DIR="$SCRIPT_DIR/linux_helix_data"
HELIX_BIN="$PROJECT_ROOT/dist/helix-linux-amd64"
DOCKER_IMAGE="helix-eval-env"

# Ensure Helix Linux binary exists
if [! -f "$HELIX_BIN" ]; then
    echo "❌ Error: Linux binary not found at $HELIX_BIN"
    echo "💡 Run 'make eval-build-linux' from the project root first."
    exit 1
fi

mkdir -p "$OUTPUT_DIR" "$LINUX_DATA_DIR"
rm -f "$OUTPUT_DIR"/*.json

if [ -n "$OPENAI_API_KEY" ]; then
    echo "$OPENAI_API_KEY" > "$LINUX_DATA_DIR/openai.key"
else
    echo "❌ Error: OPENAI_API_KEY is not exported."
    exit 1
fi

echo "🐳 Verifying evaluation environment..."
docker build -t "$DOCKER_IMAGE" - <<'EOF'
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y libasound2-dev git curl sudo \
    man-db manpages-posix rsync tar sed gawk findutils net-tools iproute2 jq expect procps
RUN yes | unminimize
RUN echo 'pcm.!default { type null }' > /root/.asoundrc
RUN echo 'ctl.!default { type null }' >> /root/.asoundrc
EOF

echo "----------------------------------------"
echo "📚 PHASE 1: Pre-building RAG Knowledge Base..."
echo "----------------------------------------"

docker run --rm -i \
    -v "$HELIX_BIN:/usr/local/bin/helix:ro" \
    -v "$LINUX_DATA_DIR:/root/.helix:rw" \
    -e OPENAI_API_KEY="$OPENAI_API_KEY" \
    "$DOCKER_IMAGE" bash -c '
        mkdir -p /root/.helix
        echo "$OPENAI_API_KEY" > /root/.helix/openai.key
        cat << "EOF_PREWARM" > /tmp/prewarm.exp
set timeout 300
spawn helix "/rag-status"
expect "Enter 1 or 2:" { send "1\r" }
expect "Use saved key or paste new?" { send "1\r" }
expect "Connecting Neural Grid Interface..."
sleep 15
send "/exit\r"
expect eof
EOF_PREWARM
        expect /tmp/prewarm.exp > /dev/null 2>&1
    '

echo "✅ RAG Index successfully pre-warmed!"

echo "----------------------------------------"
echo "🚀 PHASE 2: Starting 50-Task Evaluation..."
echo "----------------------------------------"

TASK_COUNT=0
while IFS='|' read -r TASK_ID STRATUM PROMPT EXPECTED_TOOL || [ -n "$TASK_ID" ]; do

    # Clean carriage returns
    TASK_ID=${TASK_ID//$'\r'/}
    STRATUM=${STRATUM//$'\r'/}
    PROMPT=${PROMPT//$'\r'/}
    EXPECTED_TOOL=${EXPECTED_TOOL//$'\r'/}

    # Skip header
    if [[ "$TASK_ID" == "task_id" ]] || [[ "$TASK_ID" == "TaskID" ]] || [[ -z "$TASK_ID" ]]; then
        continue
    fi

    # Trim whitespace (bash-native, no xargs)
    TASK_ID="${TASK_ID#"${TASK_ID%%[![:space:]]*}"}"
    TASK_ID="${TASK_ID%"${TASK_ID##*[![:space:]]}"}"
    STRATUM="${STRATUM#"${STRATUM%%[![:space:]]*}"}"
    STRATUM="${STRATUM%"${STRATUM##*[![:space:]]}"}"

    TASK_COUNT=$((TASK_COUNT + 1))
    echo "▶️ [Task $TASK_ID/50] (Stratum: $STRATUM) | Tool: $EXPECTED_TOOL"
    echo " Prompt: $PROMPT"

    docker run --rm \
        --name "helix_eval_$TASK_ID" \
        -v "$HELIX_BIN:/usr/local/bin/helix:ro" \
        -v "$OUTPUT_DIR:/root/.helix_telemetry:rw" \
        -v "$LINUX_DATA_DIR:/root/.helix:rw" \
        -e HELIX_TELEMETRY=1 \
        -e HELIX_TELEMETRY_PATH="/root/.helix_telemetry/telemetry_task_${TASK_ID}.json" \
        -e HELIX_TASK_ID="$TASK_ID" \
        -e OPENAI_API_KEY="$OPENAI_API_KEY" \
        "$DOCKER_IMAGE" bash -c '
            mkdir -p /root/.helix /tmp/eval_env
            echo "$OPENAI_API_KEY" > /root/.helix/openai.key
            cd /tmp/eval_env
            touch data.csv sales.txt script.sh config.ini payload.json package.json README.md auth.log syslog
            mkdir -p src data local_dir remote_dir web_root build_output /var/log

            cat << "EOF_EXPECT" > /tmp/run.exp
spawn helix [lindex $argv 0]
set timeout 30
expect {
    "Enter 1 or 2:" { send "1\r"; exp_continue }
    "Use saved key or paste new?" { send "1\r"; exp_continue }
    -re "(?i)y/n" { send "y\r"; exp_continue }
    -re "(?i)confirm" { send "y\r"; exp_continue }
    timeout { send "/exit\r"; exp_continue }
    eof {}
}
catch wait
EOF_EXPECT
            expect /tmp/run.exp "$1" > /dev/null 2>&1
        ' _ "$PROMPT"

    if [ -f "$OUTPUT_DIR/telemetry_task_${TASK_ID}.json" ]; then
        echo " ✅ Telemetry captured."
    else
        echo " ❌ Failed."
    fi
done < "$DATASET_FILE"

echo "----------------------------------------"
echo "🎉 Evaluation Complete! Processed $TASK_COUNT tasks."
echo "📊 Telemetry saved to: $OUTPUT_DIR"
ACTUAL_COUNT=$(ls -1 "$OUTPUT_DIR"/telemetry_task_*.json 2>/dev/null | wc -l | tr -d ' ')
echo "Files generated: $ACTUAL_COUNT/50"