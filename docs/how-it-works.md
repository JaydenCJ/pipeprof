# How pipeprof measures a pipeline

pipeprof never rewrites your pipeline and never injects anything into the
stages themselves. It parses the pipeline string, launches every stage as
its own process — exactly the processes a shell would launch — and owns
the pipes between them. On every boundary sits a counting tap.

## The boundary tap

For a pipeline with N stages there are N+1 boundaries:

```
stdin ──[tap 0]──> stage 1 ──[tap 1]──> stage 2 ──[tap 2]──> … ──[tap N]──> stdout
```

Each tap is a userspace pump: it reads from the upstream pipe, writes to
the downstream pipe, and counts what crossed. A chunk is counted only
after the downstream write succeeded, so the numbers describe bytes that
actually crossed the boundary, not bytes that died in a buffer.

Stage *i* reports `IN` = tap *i* and `OUT` = tap *i+1*; adjacent stages
share the boundary they are connected by, so the table always adds up.

## What is measured, and how

| Measurement | Source |
|---|---|
| bytes / records per boundary | the tap; records end at `\n` (`--records lines`), NUL (`nul`), or are disabled (`none`); a trailing partial record counts |
| wall time per stage | `Start()` → `Wait()` for that process; stages overlap, that is the nature of a pipe |
| CPU time (user+sys) | `getrusage` via the process state after `Wait()` |
| peak RSS | `ru_maxrss`, normalized to KiB (JSON only) |
| time to first output | pipeline start → first byte through the stage's output tap |
| exit code / signal | `Wait()` status; signal deaths report the signal name and the shell-style `128+n` code |

## Shell-faithful failure semantics

- **Early exit downstream** — when a stage exits (`head`), the tap's write
  fails, the tap closes the upstream read end, and the upstream process
  dies from SIGPIPE on its next write, exactly as in `yes | head -3`.
- **Start failure** — a stage that cannot start is reported (exit 127 plus
  a note), its neighbors see instant EOF/EPIPE, and the rest of the
  pipeline still runs and is still measured.
- **Exit code** — the pipeline's code is the last stage's, or the
  rightmost failing stage's with `--pipefail`, matching `set -o pipefail`.

## The bottleneck verdict

The verdict names the stage with the largest share of the pipeline's
total CPU time, and — when the stage produced output — how far into the
run its first byte appeared. A `sort` that cannot emit until it has seen
all input shows a first-output percentage near 100%, which is the
latency signature to look for. When no stage used measurable CPU the
verdict honestly reports none rather than inventing one.

## Overhead and caveats

- Every boundary adds one userspace copy (64 KiB buffer). On a typical
  machine that costs on the order of GiB/s per boundary — far above what
  the stages themselves usually sustain — but pipeprof is a profiler, not
  a benchmark: treat wall times as one observed run.
- Stage wall times overlap; they do not sum to the pipeline total.
- `IN` counts what crossed the tap. If a stage exits early, up to one
  buffer of upstream data may be read but never delivered; it is not
  counted.
- A terminal is never wired to stage 1 (stages read EOF instead), so an
  interactive `pipeprof 'cat'` cannot hang waiting for keystrokes. Use
  `--input FILE` or pipe data in.

## JSON schema (`schema_version` 1)

Top level: `tool`, `version`, `schema_version`, `pipeline`,
`records_mode`, `pipefail`, `wall_ms`, `exit_code`, `timed_out`,
`output_error?`, `stages[]`, `bottleneck` (object or null).

Per stage: `index`, `command`, `argv?`, `exit_code`, `signal?`,
`start_error?`, `wall_ms`, `user_ms`, `sys_ms`, `max_rss_kb`, `in_bytes`,
`in_records`, `out_bytes`, `out_records`, `first_output_ms` (−1 = no
output), `throughput_bps`. Record counts are −1 in `--records none`
mode. Adding fields is backwards-compatible; changing any of the above
bumps `schema_version`.
