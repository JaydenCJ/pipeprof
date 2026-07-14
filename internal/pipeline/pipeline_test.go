// Integration tests: real processes, real pipes, deterministic inputs.
// Every case uses ubiquitous POSIX tools (cat, tr, grep, sort, head, wc,
// sh, true, false, yes) and asserts on counts and exit codes — never on
// absolute timing, which would flake.
package pipeline

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/pipeprof/internal/meter"
	"github.com/JaydenCJ/pipeprof/internal/splitcmd"
)

type runOpt func(*Options)

func withStdin(s string) runOpt {
	return func(o *Options) { o.Stdin = strings.NewReader(s) }
}

func withMode(m meter.Mode) runOpt { return func(o *Options) { o.Mode = m } }
func withPipefail() runOpt         { return func(o *Options) { o.Pipefail = true } }
func withShell() runOpt            { return func(o *Options) { o.Shell = "sh" } }

// run parses and executes expr, capturing final output into the returned
// buffer. Shell mode is inferred from the Shell option being set.
func run(t *testing.T, expr string, opts ...runOpt) (*Result, *bytes.Buffer) {
	t.Helper()
	o := Options{Pipeline: expr, Stdout: &bytes.Buffer{}}
	for _, opt := range opts {
		opt(&o)
	}
	stages, err := splitcmd.Parse(expr, o.Shell != "")
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	o.Stages = stages
	res, err := Run(o)
	if err != nil {
		t.Fatalf("run %q: %v", expr, err)
	}
	return res, o.Stdout.(*bytes.Buffer)
}

func TestSingleStagePassesDataThrough(t *testing.T) {
	res, out := run(t, "cat", withStdin("hello\nworld\n"))
	if out.String() != "hello\nworld\n" {
		t.Fatalf("output = %q", out.String())
	}
	st := res.Stages[0]
	if st.In.Bytes != 12 || st.Out.Bytes != 12 {
		t.Fatalf("bytes in/out = %d/%d, want 12/12", st.In.Bytes, st.Out.Bytes)
	}
	if st.In.Records != 2 || st.Out.Records != 2 {
		t.Fatalf("records in/out = %d/%d, want 2/2", st.In.Records, st.Out.Records)
	}
	if res.ExitCode != 0 || st.ExitCode != 0 {
		t.Fatalf("exit codes: pipeline=%d stage=%d", res.ExitCode, st.ExitCode)
	}
}

func TestTransformStageChangesBytesNotRecords(t *testing.T) {
	res, out := run(t, "tr -d aeiou", withStdin("banana\npapaya\n"))
	if out.String() != "bnn\nppy\n" {
		t.Fatalf("output = %q", out.String())
	}
	st := res.Stages[0]
	if st.In.Bytes != 14 || st.Out.Bytes != 8 {
		t.Fatalf("bytes in/out = %d/%d", st.In.Bytes, st.Out.Bytes)
	}
	if st.Out.Records != 2 {
		t.Fatalf("records out = %d", st.Out.Records)
	}
}

func TestThreeStageBoundaryCounts(t *testing.T) {
	input := "alpha\nbeta\nbanana\ncherry\n"
	res, out := run(t, "cat | grep an | wc -l", withStdin(input))
	if strings.TrimSpace(out.String()) != "1" {
		t.Fatalf("final output = %q, want 1", out.String())
	}
	// Boundary 1: everything. Boundary 2: only "banana\n" (7 bytes).
	if got := res.Stages[0].Out.Bytes; got != int64(len(input)) {
		t.Fatalf("stage 1 out bytes = %d, want %d", got, len(input))
	}
	if got := res.Stages[1].Out.Bytes; got != 7 {
		t.Fatalf("stage 2 out bytes = %d, want 7", got)
	}
	if got := res.Stages[1].Out.Records; got != 1 {
		t.Fatalf("stage 2 out records = %d, want 1", got)
	}
	if got := res.Stages[2].Out.Records; got != 1 {
		t.Fatalf("stage 3 out records = %d, want 1", got)
	}
}

func TestAdjacentStagesShareBoundaryStats(t *testing.T) {
	res, _ := run(t, "cat | cat", withStdin("shared\n"))
	if res.Stages[0].Out != res.Stages[1].In {
		t.Fatalf("stage 1 Out (%+v) must equal stage 2 In (%+v)",
			res.Stages[0].Out, res.Stages[1].In)
	}
}

func TestExitCodeIsLastStageByDefault(t *testing.T) {
	res, _ := run(t, "false | cat", withStdin(""))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (last stage wins without pipefail)", res.ExitCode)
	}
	if res.Stages[0].ExitCode != 1 {
		t.Fatalf("stage 1 exit = %d, want 1", res.Stages[0].ExitCode)
	}
}

func TestPipefailSurfacesRightmostFailure(t *testing.T) {
	res, _ := run(t, "false | cat", withStdin(""), withPipefail())
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1 under pipefail", res.ExitCode)
	}
	// With two failing stages the rightmost one wins, like bash.
	res, _ = run(t, `sh -c 'exit 3' | sh -c 'cat; exit 5'`, withStdin(""), withPipefail())
	if res.ExitCode != 5 {
		t.Fatalf("exit = %d, want 5 (rightmost failing stage)", res.ExitCode)
	}
}

func TestGrepNoMatchExitCode(t *testing.T) {
	res, _ := run(t, "grep zzz", withStdin("nothing here\n"))
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want grep's 1 on no match", res.ExitCode)
	}
}

func TestStartFailureIsReportedNotFatal(t *testing.T) {
	res, _ := run(t, "definitely-not-a-command-9f2c | wc -l", withStdin(""))
	st := res.Stages[0]
	if st.StartErr == "" || st.ExitCode != 127 {
		t.Fatalf("stage 1 = %+v, want StartErr set and exit 127", st)
	}
	// The downstream stage must still run to completion on empty input.
	if res.Stages[1].ExitCode != 0 {
		t.Fatalf("stage 2 exit = %d, want 0", res.Stages[1].ExitCode)
	}
	if res.Stages[1].Out.Records != 1 { // wc prints "0\n"
		t.Fatalf("stage 2 records = %d, want 1", res.Stages[1].Out.Records)
	}
}

func TestEarlyExitPropagatesSigpipeUpstream(t *testing.T) {
	// The shell contract: when head exits, yes must die from SIGPIPE.
	// If this fails the pipeline would run forever.
	res, _ := run(t, "yes | head -3")
	if res.Stages[0].Signal != "SIGPIPE" {
		t.Fatalf("stage 1 signal = %q, want SIGPIPE", res.Stages[0].Signal)
	}
	if res.Stages[0].ExitCode != 128+13 {
		t.Fatalf("stage 1 exit = %d, want 141", res.Stages[0].ExitCode)
	}
	if res.Stages[1].Out.Records != 3 {
		t.Fatalf("head produced %d records, want 3", res.Stages[1].Out.Records)
	}
	if res.ExitCode != 0 {
		t.Fatalf("pipeline exit = %d, want 0 (head succeeded)", res.ExitCode)
	}
}

func TestNulAndNoneRecordModes(t *testing.T) {
	res, _ := run(t, "cat", withStdin("a\x00b\x00c"), withMode(meter.Nul))
	if got := res.Stages[0].Out.Records; got != 3 {
		t.Fatalf("nul records = %d, want 3 (two complete + one partial)", got)
	}
	res, _ = run(t, "cat", withStdin("a\nb\n"), withMode(meter.None))
	if got := res.Stages[0].Out.Records; got != -1 {
		t.Fatalf("none records = %d, want -1", got)
	}
	if got := res.Stages[0].Out.Bytes; got != 4 {
		t.Fatalf("none bytes = %d, want 4", got)
	}
}

func TestNilStreamsAreSafeDefaults(t *testing.T) {
	// nil stdin → the stage reads immediate EOF; nil stdout → the data
	// is discarded but the boundary still counts it.
	res, out := run(t, "cat")
	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
	if res.HasStdin {
		t.Fatal("HasStdin must be false when no stdin was wired")
	}
	if res.Stages[0].In.Bytes != 0 || res.ExitCode != 0 {
		t.Fatalf("got in=%d exit=%d", res.Stages[0].In.Bytes, res.ExitCode)
	}

	stages, err := splitcmd.Parse("cat", false)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := Run(Options{
		Pipeline: "cat",
		Stages:   stages,
		Stdin:    strings.NewReader("counted anyway\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Stages[0].Out.Bytes != 15 {
		t.Fatalf("out bytes = %d, want 15 even when discarded", res2.Stages[0].Out.Bytes)
	}
}

func TestOutputMatchesShellEquivalent(t *testing.T) {
	// The profiled pipeline must produce byte-identical output to the
	// same pipeline run by a shell — instrumentation may not alter data.
	input := "cherry\nbanana\napple\nbanana\n"
	res, out := run(t, "sort | tr a-z A-Z", withStdin(input))
	want := "APPLE\nBANANA\nBANANA\nCHERRY\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	if res.Stages[1].Out.Records != 4 {
		t.Fatalf("records = %d", res.Stages[1].Out.Records)
	}
}

func TestLargePayloadExactByteCounts(t *testing.T) {
	// 1 MiB crosses many pump buffers; counts must stay exact.
	payload := strings.Repeat(strings.Repeat("y", 127)+"\n", 8192)
	res, out := run(t, "cat | cat", withStdin(payload))
	if int64(out.Len()) != int64(len(payload)) {
		t.Fatalf("output %d bytes, want %d", out.Len(), len(payload))
	}
	for i, st := range res.Stages {
		if st.Out.Bytes != int64(len(payload)) {
			t.Fatalf("stage %d out bytes = %d, want %d", i+1, st.Out.Bytes, len(payload))
		}
		if st.Out.Records != 8192 {
			t.Fatalf("stage %d records = %d, want 8192", i+1, st.Out.Records)
		}
	}
}

func TestShellModeStageWithRedirect(t *testing.T) {
	res, out := run(t, "wc -l < /dev/null | tr -d ' '", withShell())
	if strings.TrimSpace(out.String()) != "0" {
		t.Fatalf("output = %q, want 0", out.String())
	}
	if res.Stages[0].Argv != nil {
		t.Fatal("shell-mode stages must not carry argv")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
}

func TestStderrIsCapturedPerRun(t *testing.T) {
	stages, err := splitcmd.Parse(`sh -c 'echo oops >&2; exit 4'`, false)
	if err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	res, err := Run(Options{Pipeline: "x", Stages: stages, Stderr: &errBuf})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "oops") {
		t.Fatalf("stderr = %q, want it to contain oops", errBuf.String())
	}
	if res.ExitCode != 4 {
		t.Fatalf("exit = %d, want 4", res.ExitCode)
	}
}

func TestTimeoutKillsAStalledPipeline(t *testing.T) {
	// Stdin is a pipe that never delivers data or EOF, so cat would wait
	// forever; the timeout must kill it and mark the run. The assertion
	// is on the outcome, never on elapsed time.
	pr, pw := io.Pipe()
	defer pw.Close()
	stages, err := splitcmd.Parse("cat", false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{
		Pipeline: "cat",
		Stages:   stages,
		Stdin:    pr,
		Timeout:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatal("TimedOut must be set")
	}
	if res.Stages[0].Signal != "SIGKILL" {
		t.Fatalf("signal = %q, want SIGKILL", res.Stages[0].Signal)
	}
}

func TestWallCPUAndRSSAreMeasured(t *testing.T) {
	res, _ := run(t, "cat | wc -c", withStdin("measurable\n"))
	if res.Wall <= 0 {
		t.Fatalf("pipeline wall = %v, want > 0", res.Wall)
	}
	for i, st := range res.Stages {
		if st.Wall <= 0 {
			t.Fatalf("stage %d wall = %v, want > 0", i+1, st.Wall)
		}
		if st.User < 0 || st.Sys < 0 {
			t.Fatalf("stage %d negative CPU time", i+1)
		}
		if st.MaxRSSKB <= 0 {
			t.Fatalf("stage %d MaxRSSKB = %d, want > 0 for a real process", i+1, st.MaxRSSKB)
		}
	}
}

func TestFirstOutIsSetForProducersAndUnsetForSilentStages(t *testing.T) {
	res, _ := run(t, "true | wc -c", withStdin(""))
	if res.Stages[0].FirstOut != -1 {
		t.Fatalf("silent stage FirstOut = %v, want -1", res.Stages[0].FirstOut)
	}
	if res.Stages[1].FirstOut < 0 {
		t.Fatalf("wc FirstOut = %v, want >= 0", res.Stages[1].FirstOut)
	}
}

func TestSignaledStageReportsShellStyleExitCode(t *testing.T) {
	res, _ := run(t, `sh -c 'kill -TERM $$'`)
	st := res.Stages[0]
	if st.Signal != "SIGTERM" {
		t.Fatalf("signal = %q, want SIGTERM", st.Signal)
	}
	if st.ExitCode != 128+15 {
		t.Fatalf("exit = %d, want 143", st.ExitCode)
	}
	if res.ExitCode != 143 {
		t.Fatalf("pipeline exit = %d, want 143", res.ExitCode)
	}
}

func TestEmptyStageListIsRejected(t *testing.T) {
	if _, err := Run(Options{}); err == nil {
		t.Fatal("Run without stages must fail")
	}
}

func TestResultEchoesConfiguration(t *testing.T) {
	res, _ := run(t, "cat", withStdin("x"), withMode(meter.Nul), withPipefail())
	if res.Pipeline != "cat" || res.Mode != meter.Nul || !res.Pipefail || !res.HasStdin {
		t.Fatalf("result config not echoed: %+v", res)
	}
}
