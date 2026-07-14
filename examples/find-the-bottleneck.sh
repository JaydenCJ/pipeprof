#!/usr/bin/env bash
# Which stage makes this log pipeline slow? Generate a deterministic
# 200k-line access log, then let pipeprof point at the guilty stage.
#
# Set PIPEPROF to a local build if pipeprof is not on PATH:
#   PIPEPROF=./pipeprof bash examples/find-the-bottleneck.sh
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG="$(mktemp)"
trap 'rm -f "$LOG"' EXIT

bash "$DIR/make-demo-log.sh" "$LOG" 200000 > /dev/null

# The classic "top offenders" pipeline: filter server errors, extract the
# request path, rank distinct paths. One of these five stages dominates —
# pipeprof names it instead of leaving you to guess.
"${PIPEPROF:-pipeprof}" --input "$LOG" --no-output \
  'grep " 500 " | cut -d" " -f7 | sort | uniq -c | sort -rn'
