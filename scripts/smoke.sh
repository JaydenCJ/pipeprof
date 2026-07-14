#!/usr/bin/env bash
# End-to-end smoke test for pipeprof: builds the binary, profiles real
# pipelines over deterministic inputs, and asserts on the actual CLI
# output, report content and exit codes. No network, idempotent,
# finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/pipeprof"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/pipeprof) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "pipeprof 0.1.0" || fail "--version mismatch"

echo "3. fabricate a deterministic input file"
seq 1 5000 | sed 's/^/record /' > "$WORKDIR/input.txt"

echo "4. profiled pipeline output is byte-identical to the shell's"
"$BIN" --input "$WORKDIR/input.txt" --report "$WORKDIR/report.txt" \
  'grep record | tr a-z A-Z | wc -l' > "$WORKDIR/got.txt" \
  || fail "pipeline run failed"
sh -c 'grep record | tr a-z A-Z | wc -l' < "$WORKDIR/input.txt" > "$WORKDIR/want.txt"
cmp -s "$WORKDIR/got.txt" "$WORKDIR/want.txt" || fail "instrumentation altered the data"

echo "5. stage table reports per-boundary counts"
grep -q "pipeprof — 3 stages" "$WORKDIR/report.txt" || fail "missing report header"
grep -q "grep record" "$WORKDIR/report.txt" || fail "stage 1 row missing"
grep -q "5,000" "$WORKDIR/report.txt" || fail "record count 5,000 missing"
grep -q "bottleneck:" "$WORKDIR/report.txt" || fail "bottleneck verdict missing"

echo "6. JSON report is machine-readable and correct"
"$BIN" --input "$WORKDIR/input.txt" --no-output --json \
  --report "$WORKDIR/report.json" 'grep record | wc -l' || fail "json run failed"
grep -q '"tool": "pipeprof"' "$WORKDIR/report.json" || fail "json envelope missing"
grep -q '"schema_version": 1' "$WORKDIR/report.json" || fail "schema version missing"
grep -q '"out_records": 5000' "$WORKDIR/report.json" || fail "json record count wrong"

echo "7. exit codes mirror the pipeline"
printf 'no match here\n' | "$BIN" --no-output 'grep zzz' 2>/dev/null && rc=0 || rc=$?
[ "$rc" -eq 1 ] || fail "grep no-match should exit 1, got $rc"
"$BIN" --no-output 'false | cat' 2>/dev/null || fail "last stage wins without --pipefail"
if "$BIN" --pipefail --no-output 'false | cat' 2>/dev/null; then
  fail "--pipefail should surface the false"
fi

echo "8. early downstream exit sends SIGPIPE upstream, shell-style"
"$BIN" --no-output 'yes | head -3' 2> "$WORKDIR/sigpipe.txt" \
  || fail "yes|head should exit 0 (head succeeded)"
grep -q "SIGPIPE" "$WORKDIR/sigpipe.txt" || fail "SIGPIPE missing from report"

echo "9. NUL-delimited record counting"
printf 'a\0b\0c' | "$BIN" --records nul --no-output 'cat' 2> "$WORKDIR/nul.txt" \
  || fail "nul run failed"
grep -qE '(^|[^0-9])3([^0-9]|$)' "$WORKDIR/nul.txt" || fail "3 NUL records missing"

echo "10. usage errors exit 2 and hint at --shell"
set +e
"$BIN" 'sort > out.txt' 2> "$WORKDIR/usage.txt"
rc=$?
set -e
[ "$rc" -eq 2 ] || fail "redirect without --shell should exit 2, got $rc"
grep -q -- "--shell" "$WORKDIR/usage.txt" || fail "usage error must hint at --shell"

echo "11. --shell enables per-stage redirects"
OUT="$("$BIN" --shell 'wc -l < /dev/null | tr -d " "' 2>/dev/null)"
[ "$OUT" = "0" ] || fail "--shell stage output = $OUT, want 0"

echo "SMOKE OK"
