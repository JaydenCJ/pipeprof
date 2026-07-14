# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-12

### Added

- Quote-aware pipeline parsing: single/double quotes, backslash escapes,
  `$(…)` and backtick substitution never split; unsupported shell
  metacharacters are rejected with an error that points at `--shell`.
- Per-boundary counting taps measuring bytes and records (newline, NUL
  via `--records nul`, or disabled via `--records none`), with trailing
  partial records counted and chunking-independent totals.
- Per-stage measurements: wall time, user/sys CPU time, peak RSS,
  time to first output, exit code, and signal name for signal deaths.
- Shell-faithful semantics: byte-identical passthrough output, SIGPIPE
  propagation on early downstream exit, exit code of the last stage or
  the rightmost failure with `--pipefail`, graceful degradation when a
  stage fails to start (exit 127 plus a report note).
- Stage table renderer with human units, aligned columns, command
  truncation (`--wide` to disable), notes, and a bottleneck verdict
  (largest CPU share + first-output percentage), plus a stable JSON
  report (`schema_version` 1) with per-stage throughput.
- Stream control: `--input`, `--output`, `--no-output`, `--report`,
  report on stderr by default so pipeprof can sit inside a larger pipe;
  terminals are never wired to stage 1.
- `--shell` mode running each stage via `sh -c` for globs, expansion and
  redirections; `--timeout` killing a stalled pipeline with exit 124.
- Runnable examples (deterministic demo-log generator, bottleneck demo,
  JSON extraction) and a measurement-methodology doc
  (`docs/how-it-works.md`).
- 90 deterministic offline tests (unit + in-process CLI integration
  against real processes) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/pipeprof/releases/tag/v0.1.0
