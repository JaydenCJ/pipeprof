// Tests for formatting helpers and both renderers, driven by hand-built
// fixtures so every cell of the table is asserted without running a
// single process — rendering must be a pure function of the Result.
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/pipeprof/internal/meter"
	"github.com/JaydenCJ/pipeprof/internal/pipeline"
)

// fixture models `cat access.log | grep ERROR | sort` with plausible,
// fully deterministic numbers.
func fixture() *pipeline.Result {
	return &pipeline.Result{
		Pipeline: "cat access.log | grep ERROR | sort",
		Wall:     1240 * time.Millisecond,
		ExitCode: 0,
		Mode:     meter.Lines,
		HasStdin: false,
		Stages: []pipeline.StageResult{
			{
				Index: 1, Command: "cat access.log",
				Argv: []string{"cat", "access.log"},
				Wall: 1210 * time.Millisecond, User: 20 * time.Millisecond, Sys: 60 * time.Millisecond,
				MaxRSSKB: 2048,
				Out:      meter.Stats{Bytes: 50529027, Records: 120000},
				FirstOut: 300 * time.Microsecond,
			},
			{
				Index: 2, Command: "grep ERROR",
				Argv: []string{"grep", "ERROR"},
				Wall: 1220 * time.Millisecond, User: 300 * time.Millisecond, Sys: 10 * time.Millisecond,
				MaxRSSKB: 2100,
				In:       meter.Stats{Bytes: 50529027, Records: 120000},
				Out:      meter.Stats{Bytes: 1153434, Records: 3120},
				FirstOut: 45 * time.Millisecond,
			},
			{
				Index: 3, Command: "sort",
				Argv: []string{"sort"},
				Wall: 1230 * time.Millisecond, User: 400 * time.Millisecond, Sys: 20 * time.Millisecond,
				MaxRSSKB: 8192,
				In:       meter.Stats{Bytes: 1153434, Records: 3120},
				Out:      meter.Stats{Bytes: 1153434, Records: 3120},
				FirstOut: 1190 * time.Millisecond,
			},
		},
	}
}

func renderText(res *pipeline.Result, wide bool) string {
	var b bytes.Buffer
	RenderText(&b, res, wide)
	return b.String()
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0B",
		1:             "1B",
		1023:          "1023B",
		1024:          "1.0KiB",
		1536:          "1.5KiB",
		1048576:       "1.0MiB",
		50529027:      "48.2MiB",
		1153434:       "1.1MiB",
		107374182400:  "100GiB",
		1099511627776: "1.0TiB",
		-1:            "—",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Fatalf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                      "0.0ms",
		300 * time.Microsecond: "0.3ms",
		9*time.Millisecond + 940*time.Microsecond: "9.9ms",
		45 * time.Millisecond:                     "45ms",
		999 * time.Millisecond:                    "999ms",
		1240 * time.Millisecond:                   "1.24s",
		59 * time.Second:                          "59.00s",
		61500 * time.Millisecond:                  "1m01.5s",
		-time.Second:                              "—",
	}
	for in, want := range cases {
		if got := HumanDuration(in); got != want {
			t.Fatalf("HumanDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestComma(t *testing.T) {
	cases := map[int64]string{
		0:       "0",
		999:     "999",
		1000:    "1,000",
		120000:  "120,000",
		1234567: "1,234,567",
		-1234:   "-1,234",
	}
	for in, want := range cases {
		if got := Comma(in); got != want {
			t.Fatalf("Comma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("exactly-ten", 11); got != "exactly-ten" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("abcdefghij", 5); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
	// Truncation counts runes, not bytes — multibyte text must not be cut
	// mid-character.
	if got := Truncate("日本語のテキスト", 4); got != "日本語…" {
		t.Fatalf("got %q", got)
	}
}

func TestTextHeaderAndTableCells(t *testing.T) {
	out := renderText(fixture(), false)
	if !strings.Contains(out, "pipeprof — 3 stages, 1.24s total, exit 0") {
		t.Fatalf("header missing:\n%s", out)
	}
	for _, want := range []string{
		"COMMAND", "IN BYTES", "OUT BYTES", "RECORDS", "WALL", "CPU", "FIRST OUT", "EXIT",
		"cat access.log", "48.2MiB", "120,000", "1.21s", "80ms",
		"grep ERROR", "1.1MiB", "3,120", "310ms", "45ms",
		"sort", "420ms", "1.19s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
}

func TestTextFirstStageWithoutStdinShowsDash(t *testing.T) {
	out := renderText(fixture(), false)
	line := stageLine(t, out, "cat access.log")
	if !strings.Contains(line, "—") {
		t.Fatalf("stage 1 IN BYTES should be a dash without stdin: %q", line)
	}
}

func TestTextBottleneckPicksLargestCPUShare(t *testing.T) {
	out := renderText(fixture(), false)
	if !strings.Contains(out, "bottleneck: stage 3 (sort) — 51.9% of pipeline CPU") {
		t.Fatalf("bottleneck line wrong:\n%s", out)
	}
	if !strings.Contains(out, "first output after 96% of the run") {
		t.Fatalf("first-output clause missing:\n%s", out)
	}
	// The exported helper must agree with the rendered verdict.
	i, share, ok := Bottleneck(fixture())
	if !ok || i != 2 {
		t.Fatalf("Bottleneck = (%d, %v, %v), want stage index 2", i, share, ok)
	}
	if share < 0.51 || share > 0.53 {
		t.Fatalf("share = %v, want ≈0.519", share)
	}
}

func TestBottleneckNoneWhenNoCPUMeasured(t *testing.T) {
	res := fixture()
	for i := range res.Stages {
		res.Stages[i].User, res.Stages[i].Sys = 0, 0
	}
	out := renderText(res, false)
	if !strings.Contains(out, "bottleneck: none detected") {
		t.Fatalf("expected honest no-bottleneck line:\n%s", out)
	}
}

func TestBottleneckOmittedForSingleStage(t *testing.T) {
	res := fixture()
	res.Stages = res.Stages[:1]
	out := renderText(res, false)
	if strings.Contains(out, "bottleneck") {
		t.Fatalf("single-stage run must not print a bottleneck:\n%s", out)
	}
	if !strings.Contains(out, "1 stage,") {
		t.Fatalf("singular header missing:\n%s", out)
	}
}

func TestTextSignalShownInsteadOfExitCode(t *testing.T) {
	res := fixture()
	res.Stages[0].Signal = "SIGPIPE"
	res.Stages[0].ExitCode = 141
	out := renderText(res, false)
	line := stageLine(t, out, "cat access.log")
	if !strings.HasSuffix(strings.TrimRight(line, "\n"), "SIGPIPE") {
		t.Fatalf("exit cell should read SIGPIPE: %q", line)
	}
}

func TestTextStartErrorNote(t *testing.T) {
	res := fixture()
	res.Stages[1].StartErr = `exec: "grep": executable file not found in $PATH`
	res.Stages[1].ExitCode = 127
	out := renderText(res, false)
	if !strings.Contains(out, `! stage 2 failed to start: exec: "grep"`) {
		t.Fatalf("start-error note missing:\n%s", out)
	}
	line := stageLine(t, out, "grep ERROR")
	if !strings.Contains(line, "—") {
		t.Fatalf("failed stage should dash out wall/cpu: %q", line)
	}
}

func TestTextTimedOutMarkers(t *testing.T) {
	res := fixture()
	res.TimedOut = true
	out := renderText(res, false)
	if !strings.Contains(out, "TIMED OUT") {
		t.Fatalf("timed-out marker missing:\n%s", out)
	}
	if !strings.Contains(out, "killed by --timeout") {
		t.Fatalf("timeout note missing:\n%s", out)
	}
}

func TestTextRecordsDashInNoneMode(t *testing.T) {
	res := fixture()
	res.Mode = meter.None
	for i := range res.Stages {
		res.Stages[i].In.Records = -1
		res.Stages[i].Out.Records = -1
	}
	out := renderText(res, false)
	line := stageLine(t, out, "grep ERROR")
	if strings.Contains(line, "3,120") {
		t.Fatalf("records must not appear in none mode: %q", line)
	}
}

func TestTextLongCommandTruncatedUnlessWide(t *testing.T) {
	res := fixture()
	long := "awk -F, '{ sum[$3] += $5 } END { for (k in sum) print k, sum[k] }'"
	res.Stages[1].Command = long
	narrow := renderText(res, false)
	if strings.Contains(narrow, long) {
		t.Fatalf("long command should be truncated by default:\n%s", narrow)
	}
	if !strings.Contains(narrow, "…") {
		t.Fatalf("truncation marker missing:\n%s", narrow)
	}
	wide := renderText(res, true)
	if !strings.Contains(wide, long) {
		t.Fatalf("--wide must keep the full command:\n%s", wide)
	}
}

func TestJSONEnvelopeAndStageFields(t *testing.T) {
	var b bytes.Buffer
	if err := RenderJSON(&b, fixture()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(b.Bytes(), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if envelope["tool"] != "pipeprof" || envelope["schema_version"] != float64(1) {
		t.Fatalf("envelope wrong: %v", envelope)
	}
	if envelope["records_mode"] != "lines" {
		t.Fatalf("records_mode = %v", envelope["records_mode"])
	}
	var doc struct {
		Stages []struct {
			Index         int      `json:"index"`
			Command       string   `json:"command"`
			Argv          []string `json:"argv"`
			OutBytes      int64    `json:"out_bytes"`
			OutRecords    int64    `json:"out_records"`
			WallMS        float64  `json:"wall_ms"`
			UserMS        float64  `json:"user_ms"`
			FirstOutputMS float64  `json:"first_output_ms"`
			ThroughputBPS int64    `json:"throughput_bps"`
			MaxRSSKB      int64    `json:"max_rss_kb"`
		} `json:"stages"`
		Bottleneck struct {
			Stage    int     `json:"stage"`
			CPUShare float64 `json:"cpu_share"`
		} `json:"bottleneck"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	s2 := doc.Stages[1]
	if s2.Index != 2 || s2.Command != "grep ERROR" || len(s2.Argv) != 2 {
		t.Fatalf("stage 2 identity wrong: %+v", s2)
	}
	if s2.OutBytes != 1153434 || s2.OutRecords != 3120 {
		t.Fatalf("stage 2 counts wrong: %+v", s2)
	}
	if s2.WallMS != 1220 || s2.UserMS != 300 || s2.FirstOutputMS != 45 {
		t.Fatalf("stage 2 timings wrong: %+v", s2)
	}
	wallSeconds := 1.22
	if want := int64(float64(1153434) / wallSeconds); s2.ThroughputBPS != want {
		t.Fatalf("throughput = %d, want %d", s2.ThroughputBPS, want)
	}
	if doc.Bottleneck.Stage != 3 || doc.Bottleneck.CPUShare != 0.519 {
		t.Fatalf("bottleneck wrong: %+v", doc.Bottleneck)
	}
	if doc.Stages[2].MaxRSSKB != 8192 {
		t.Fatalf("max_rss_kb = %d", doc.Stages[2].MaxRSSKB)
	}
}

func TestJSONHonestAbsenceMarkers(t *testing.T) {
	// A stage that never produced output reports -1, and a run with no
	// measurable CPU reports a null bottleneck — the JSON must never
	// fake a value where nothing was measured.
	res := fixture()
	res.Stages[0].FirstOut = -1
	var b bytes.Buffer
	if err := RenderJSON(&b, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"first_output_ms": -1`) {
		t.Fatalf("silent stage should report -1:\n%s", b.String())
	}

	res = fixture()
	for i := range res.Stages {
		res.Stages[i].User, res.Stages[i].Sys = 0, 0
	}
	b.Reset()
	if err := RenderJSON(&b, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"bottleneck": null`) {
		t.Fatalf("bottleneck should be null when unmeasurable:\n%s", b.String())
	}
}

// stageLine returns the table row containing marker.
func stageLine(t *testing.T, out, marker string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) && !strings.HasPrefix(line, "!") &&
			!strings.HasPrefix(line, "bottleneck") {
			return line
		}
	}
	t.Fatalf("no table row contains %q:\n%s", marker, out)
	return ""
}
