#!/usr/bin/env bash
# Fabricate a deterministic synthetic access log. Every field derives
# from the line number — no randomness, no clock — so pipeprof demos
# reproduce byte-for-byte on every machine.
#
# usage: make-demo-log.sh [OUTFILE] [LINES]
set -euo pipefail

OUT="${1:-/tmp/pipeprof-demo.log}"
LINES="${2:-200000}"

awk -v n="$LINES" 'BEGIN {
  split("200 200 200 301 404 500", codes, " ")
  split("GET GET GET POST PUT", methods, " ")
  for (i = 1; i <= n; i++) {
    ip     = sprintf("10.0.%d.%d", i % 32, i % 251)
    code   = codes[(i % 6) + 1]
    method = methods[(i % 5) + 1]
    path   = sprintf("/api/items/%d", i % 997)
    size   = 200 + (i % 1400)
    printf "%s - - [12/Jul/2026:10:%02d:%02d +0000] \"%s %s HTTP/1.1\" %s %d\n",
           ip, int(i / 60) % 60, i % 60, method, path, code, size
  }
}' > "$OUT"

echo "wrote $LINES lines to $OUT"
