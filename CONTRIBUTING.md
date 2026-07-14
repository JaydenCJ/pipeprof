# Contributing to pipeprof

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22 and a POSIX shell with the usual coreutils; nothing else.

```bash
git clone https://github.com/JaydenCJ/pipeprof && cd pipeprof
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, profiles real pipelines over
deterministic inputs in a temp dir, and asserts on the actual table, JSON
and exit codes; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network, no sleeps).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsing, counting and rendering never spawn processes — only
   `internal/pipeline` does).

## Ground rules

- Keep dependencies at zero — the `go.mod` require list is intentionally
  empty; adding an entry needs strong justification in the PR.
- No network calls, ever. pipeprof's only external interface is the
  processes the user asked it to run. No telemetry.
- Never assert on absolute timing in tests — assert on counts, exit
  codes and outcomes. A test that flakes under load is a bug.
- The profiled pipeline must behave exactly as the shell would run it:
  byte-identical output, SIGPIPE on early exit, same exit codes. Any
  observable deviation is a correctness bug, not a nice-to-have.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `pipeprof --version`, the exact command you ran,
the report (`--json` preferred), and — for measurement bugs — the same
pipeline run directly in your shell with its exit codes
(`echo "${PIPESTATUS[@]}"` in bash), since that is the behavior pipeprof
must mirror.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
