// In-process integration tests for the CLI: every flag and exit path is
// driven through Run with explicit streams, so no PATH tricks, no golden
// terminals, and no dependence on wall-clock values.
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/pipeprof/internal/version"
)

// invoke runs the CLI with stdin content and returns exit code, stdout
// and stderr.
func invoke(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestVersionFlagAndSubcommand(t *testing.T) {
	code, out, _ := invoke(t, "", "--version")
	if code != 0 || out != "pipeprof "+version.Version+"\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = invoke(t, "", "version")
	if code != 0 || !strings.Contains(out, version.Version) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	code, out, _ := invoke(t, "", "--help")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "--records") {
		t.Fatalf("usage text incomplete:\n%s", out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	// No pipeline argument: full usage on stderr.
	code, _, errOut := invoke(t, "")
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("no args: code=%d stderr=%q", code, errOut)
	}
	// Unquoted pipeline arriving as two positionals: actionable hint.
	code, _, errOut = invoke(t, "", "cat", "wc -l")
	if code != 2 || !strings.Contains(errOut, "quote the whole pipeline") {
		t.Fatalf("two args: code=%d stderr=%q", code, errOut)
	}
	// Unknown flag.
	code, _, errOut = invoke(t, "", "--frobnicate", "cat")
	if code != 2 || !strings.Contains(errOut, "pipeprof:") {
		t.Fatalf("bad flag: code=%d stderr=%q", code, errOut)
	}
}

func TestBadFlagValuesExitTwo(t *testing.T) {
	code, _, errOut := invoke(t, "", "--records", "words", "cat")
	if code != 2 || !strings.Contains(errOut, "invalid records mode") {
		t.Fatalf("records: code=%d stderr=%q", code, errOut)
	}
	code, _, _ = invoke(t, "", "--timeout", "banana", "cat")
	if code != 2 {
		t.Fatalf("timeout: code = %d, want 2", code)
	}
	code, _, errOut = invoke(t, "", "--output", "x", "--no-output", "cat")
	if code != 2 || !strings.Contains(errOut, "mutually exclusive") {
		t.Fatalf("conflict: code=%d stderr=%q", code, errOut)
	}
	code, _, errOut = invoke(t, "", "--input", "/no/such/file.txt", "cat")
	if code != 2 || !strings.Contains(errOut, "cannot open --input") {
		t.Fatalf("input: code=%d stderr=%q", code, errOut)
	}
}

func TestUnparsablePipelineExitsTwoWithShellHint(t *testing.T) {
	code, _, errOut := invoke(t, "", "sort > out.txt")
	if code != 2 || !strings.Contains(errOut, "--shell") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestPipelineOutputPassesThroughStdout(t *testing.T) {
	code, out, errOut := invoke(t, "b\na\nc\n", "sort")
	if code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", code, errOut)
	}
	if out != "a\nb\nc\n" {
		t.Fatalf("stdout = %q, want sorted passthrough", out)
	}
	// The report lives on stderr so pipeprof can sit inside a larger pipe.
	if !strings.Contains(errOut, "pipeprof — 1 stage") {
		t.Fatalf("report missing from stderr:\n%s", errOut)
	}
}

func TestReportTableHasCountsAndBottleneck(t *testing.T) {
	code, _, errOut := invoke(t, "alpha\nbeta\nbanana\n", "grep an | wc -l")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"COMMAND", "OUT BYTES", "RECORDS", "grep an", "wc -l", "bottleneck:"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("report missing %q:\n%s", want, errOut)
		}
	}
}

func TestExitCodeMirrorsPipeline(t *testing.T) {
	code, _, _ := invoke(t, "", `sh -c 'exit 7'`)
	if code != 7 {
		t.Fatalf("code = %d, want 7", code)
	}
}

func TestPipefailFlag(t *testing.T) {
	code, _, _ := invoke(t, "", "false | cat")
	if code != 0 {
		t.Fatalf("without --pipefail: code = %d, want 0", code)
	}
	code, _, _ = invoke(t, "", "--pipefail", "false | cat")
	if code != 1 {
		t.Fatalf("with --pipefail: code = %d, want 1", code)
	}
}

func TestJSONReportOnStderrParses(t *testing.T) {
	code, out, errOut := invoke(t, "x\ny\n", "--json", "wc -l")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.TrimSpace(out) != "2" {
		t.Fatalf("stdout = %q", out)
	}
	var doc struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		ExitCode      int    `json:"exit_code"`
		Stages        []struct {
			InBytes    int64 `json:"in_bytes"`
			OutRecords int64 `json:"out_records"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(errOut), &doc); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, errOut)
	}
	if doc.Tool != "pipeprof" || doc.SchemaVersion != 1 || len(doc.Stages) != 1 {
		t.Fatalf("envelope: %+v", doc)
	}
	if doc.Stages[0].InBytes != 4 || doc.Stages[0].OutRecords != 1 {
		t.Fatalf("stage counts: %+v", doc.Stages[0])
	}
}

func TestReportFileFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	code, _, errOut := invoke(t, "data\n", "--report", path, "cat")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(errOut, "pipeprof —") {
		t.Fatalf("report leaked to stderr despite --report:\n%s", errOut)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(content), "pipeprof — 1 stage") {
		t.Fatalf("report file content:\n%s", content)
	}
}

func TestOutputFileFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	code, out, _ := invoke(t, "keep\n", "--output", path, "tr a-z A-Z")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if out != "" {
		t.Fatalf("stdout should be empty with --output: %q", out)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "KEEP\n" {
		t.Fatalf("file = %q", content)
	}
}

func TestNoOutputFlagDiscardsButCounts(t *testing.T) {
	code, out, errOut := invoke(t, "1\n2\n3\n", "--no-output", "cat")
	if code != 0 || out != "" {
		t.Fatalf("code=%d stdout=%q", code, out)
	}
	if !strings.Contains(errOut, "3") { // 3 records still measured
		t.Fatalf("report lost the counts:\n%s", errOut)
	}
}

func TestInputFileFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := invoke(t, "ignored-stdin\n", "--input", path, "cat")
	if code != 0 || out != "from-file\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRecordsModesFlowIntoReport(t *testing.T) {
	code, _, errOut := invoke(t, "a\x00b\x00c", "--records", "nul", "cat")
	if code != 0 {
		t.Fatalf("nul: code = %d", code)
	}
	if !strings.Contains(errOut, "3") {
		t.Fatalf("expected 3 NUL records in report:\n%s", errOut)
	}
	code, _, errOut = invoke(t, "a\nb\n", "--records", "none", "cat")
	if code != 0 {
		t.Fatalf("none: code = %d", code)
	}
	if !strings.Contains(errOut, "—") {
		t.Fatalf("records column should dash out in none mode:\n%s", errOut)
	}
}

func TestShellFlagEnablesRedirects(t *testing.T) {
	code, out, _ := invoke(t, "", "--shell", "wc -l < /dev/null | tr -d ' '")
	if code != 0 || strings.TrimSpace(out) != "0" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestWideFlagKeepsLongCommands(t *testing.T) {
	long := `grep -v '^#' | tr abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ`
	_, _, narrow := invoke(t, "x\n", long)
	if !strings.Contains(narrow, "…") {
		t.Fatalf("long stage should truncate by default:\n%s", narrow)
	}
	_, _, wideOut := invoke(t, "x\n", "--wide", long)
	if strings.Contains(wideOut, "…") {
		t.Fatalf("--wide must not truncate:\n%s", wideOut)
	}
}

func TestTimeoutKillsAndExits124(t *testing.T) {
	// A pipe that never sends data or EOF: cat stalls until --timeout
	// fires. Assertions cover the outcome only, never elapsed time.
	pr, pw := io.Pipe()
	defer pw.Close()
	var out, errOut bytes.Buffer
	code := Run([]string{"--timeout", "100ms", "cat"}, pr, &out, &errOut)
	if code != 124 {
		t.Fatalf("code = %d, want 124", code)
	}
	if !strings.Contains(errOut.String(), "TIMED OUT") {
		t.Fatalf("report should mark the timeout:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "SIGKILL") {
		t.Fatalf("killed stage should show SIGKILL:\n%s", errOut.String())
	}
}

func TestStageStderrReachesCallerStderr(t *testing.T) {
	_, _, errOut := invoke(t, "", `sh -c 'echo warn >&2'`)
	if !strings.Contains(errOut, "warn") {
		t.Fatalf("stage stderr lost:\n%s", errOut)
	}
}

func TestStartFailureNoteAndDegradedRun(t *testing.T) {
	code, _, errOut := invoke(t, "", "no-such-tool-3a7b | wc -l")
	if code != 0 { // wc still succeeds on the empty stream
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(errOut, "failed to start") || !strings.Contains(errOut, "127") {
		t.Fatalf("start failure not reported:\n%s", errOut)
	}
}

func TestSigpipeStoryVisibleInReport(t *testing.T) {
	code, _, errOut := invoke(t, "", "--no-output", "yes | head -2")
	if code != 0 {
		t.Fatalf("code = %d, want 0 (head succeeded)", code)
	}
	if !strings.Contains(errOut, "SIGPIPE") {
		t.Fatalf("upstream SIGPIPE missing from report:\n%s", errOut)
	}
}
