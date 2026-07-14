#!/usr/bin/env bash
# Machine-readable profiles: run with --json and extract numbers with
# nothing but grep — no jq required, though jq works great too.
#
# Set PIPEPROF to a local build if pipeprof is not on PATH.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG="$(mktemp)"
REPORT="$(mktemp)"
trap 'rm -f "$LOG" "$REPORT"' EXIT

bash "$DIR/make-demo-log.sh" "$LOG" 50000 > /dev/null

"${PIPEPROF:-pipeprof}" --input "$LOG" --no-output --json --report "$REPORT" \
  'grep " 404 " | wc -l'

echo "per-stage throughput (bytes/second):"
grep '"throughput_bps"' "$REPORT"
echo "pipeline exit code:"
grep -m1 '"exit_code"' "$REPORT"
