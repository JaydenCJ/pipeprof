# pipeprof examples

Three runnable scripts, all offline and deterministic. Build pipeprof
first (`go build -o pipeprof ./cmd/pipeprof`) and either put it on PATH
or point the scripts at it with `PIPEPROF=./pipeprof`.

## make-demo-log.sh

Fabricates a synthetic access log where every field derives from the
line number — no randomness, no clock — so the other two scripts (and
the README quickstart) reproduce byte-for-byte on any machine.

```bash
bash examples/make-demo-log.sh /tmp/demo.log 200000
```

## find-the-bottleneck.sh

The headline demo: a five-stage "top offenders" log pipeline
(`grep | cut | sort | uniq -c | sort -rn`) profiled end to end. The
stage table shows the record collapse (33,333 → 997), `sort` blocking
until nearly the end of the run, and the verdict naming the stage that
actually burned the CPU.

```bash
PIPEPROF=./pipeprof bash examples/find-the-bottleneck.sh
```

## json-report.sh

The same idea, machine-readable: profiles a pipeline with `--json` and
extracts per-stage throughput and the exit code using nothing but grep,
to show the report is trivially scriptable.

```bash
PIPEPROF=./pipeprof bash examples/json-report.sh
```
