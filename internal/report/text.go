// Text renderer: the stage table pipeprof prints to stderr after a run.
// One row per stage, one column per measurement, then the bottleneck
// verdict — the whole point of the tool in four lines of terminal output.
package report

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/JaydenCJ/pipeprof/internal/pipeline"
)

// commandWidth is the default command-column budget; --wide lifts it.
const commandWidth = 32

// RenderText writes the human-readable stage table for res to w.
func RenderText(w io.Writer, res *pipeline.Result, wide bool) {
	n := len(res.Stages)
	plural := "s"
	if n == 1 {
		plural = ""
	}
	suffix := ""
	if res.TimedOut {
		suffix = " — TIMED OUT"
	}
	fmt.Fprintf(w, "pipeprof — %d stage%s, %s total, exit %d%s\n\n",
		n, plural, HumanDuration(res.Wall), res.ExitCode, suffix)

	header := []string{"#", "COMMAND", "IN BYTES", "OUT BYTES", "RECORDS", "WALL", "CPU", "FIRST OUT", "EXIT"}
	right := []bool{true, false, true, true, true, true, true, true, true}
	rows := make([][]string, 0, n)
	for i, st := range res.Stages {
		rows = append(rows, []string{
			strconv.Itoa(st.Index),
			commandCell(st.Command, wide),
			inBytesCell(res, i),
			HumanBytes(st.Out.Bytes),
			recordsCell(st.Out.Records),
			wallCell(&st),
			cpuCell(&st),
			HumanDuration(st.FirstOut),
			exitCell(&st),
		})
	}
	writeTable(w, header, rows, right)

	notes := collectNotes(res)
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, note := range notes {
			fmt.Fprintf(w, "! %s\n", note)
		}
	}

	if n > 1 {
		fmt.Fprintf(w, "\n%s\n", bottleneckLine(res, wide))
	}
}

func commandCell(cmd string, wide bool) string {
	if wide {
		return cmd
	}
	return Truncate(cmd, commandWidth)
}

// inBytesCell shows an em dash for stage 1 when no stdin was wired at all,
// distinguishing "nothing arrived" from "no boundary existed".
func inBytesCell(res *pipeline.Result, i int) string {
	if i == 0 && !res.HasStdin {
		return "—"
	}
	return HumanBytes(res.Stages[i].In.Bytes)
}

func recordsCell(records int64) string {
	if records < 0 {
		return "—"
	}
	return Comma(records)
}

func wallCell(st *pipeline.StageResult) string {
	if st.StartErr != "" {
		return "—"
	}
	return HumanDuration(st.Wall)
}

func cpuCell(st *pipeline.StageResult) string {
	if st.StartErr != "" {
		return "—"
	}
	return HumanDuration(st.User + st.Sys)
}

// exitCell prefers the signal name over the synthetic 128+n exit code:
// "SIGPIPE" tells the story, "141" makes the reader do modular arithmetic.
func exitCell(st *pipeline.StageResult) string {
	if st.Signal != "" {
		return st.Signal
	}
	return strconv.Itoa(st.ExitCode)
}

func collectNotes(res *pipeline.Result) []string {
	var notes []string
	for _, st := range res.Stages {
		if st.StartErr != "" {
			notes = append(notes, fmt.Sprintf("stage %d failed to start: %s", st.Index, st.StartErr))
		}
	}
	if res.OutputErr != "" {
		notes = append(notes, "final output truncated: "+res.OutputErr)
	}
	if res.TimedOut {
		notes = append(notes, "pipeline killed by --timeout before completion")
	}
	return notes
}

// Bottleneck picks the stage that consumed the largest share of the
// pipeline's total CPU time. It returns ok=false when no stage used
// measurable CPU — an I/O-bound or trivially fast pipeline has no
// meaningful CPU bottleneck and the report must not invent one.
func Bottleneck(res *pipeline.Result) (index int, share float64, ok bool) {
	var total, best time.Duration
	bi := -1
	for i, st := range res.Stages {
		cpu := st.User + st.Sys
		total += cpu
		if cpu > best {
			best, bi = cpu, i
		}
	}
	if bi < 0 || total <= 0 {
		return 0, 0, false
	}
	return bi, float64(best) / float64(total), true
}

func bottleneckLine(res *pipeline.Result, wide bool) string {
	i, share, ok := Bottleneck(res)
	if !ok {
		return "bottleneck: none detected (negligible CPU time; the pipe is I/O-bound or too quick to attribute)"
	}
	st := res.Stages[i]
	line := fmt.Sprintf("bottleneck: stage %d (%s) — %.1f%% of pipeline CPU",
		st.Index, commandCell(st.Command, wide), share*100)
	if st.FirstOut >= 0 && res.Wall > 0 {
		pct := float64(st.FirstOut) / float64(res.Wall) * 100
		if pct > 100 {
			pct = 100
		}
		line += fmt.Sprintf(", first output after %.0f%% of the run", pct)
	}
	return line
}

// writeTable prints rows under header with two-space gutters, sizing each
// column to its widest cell. Width is counted in runes so em dashes and
// other multi-byte glyphs align.
func writeTable(w io.Writer, header []string, rows [][]string, right []bool) {
	widths := make([]int, len(header))
	for c, h := range header {
		widths[c] = runeLen(h)
	}
	for _, row := range rows {
		for c, cell := range row {
			if l := runeLen(cell); l > widths[c] {
				widths[c] = l
			}
		}
	}
	printRow := func(cells []string) {
		var b strings.Builder
		for c, cell := range cells {
			if c > 0 {
				b.WriteString("  ")
			}
			pad := widths[c] - runeLen(cell)
			if right[c] {
				b.WriteString(strings.Repeat(" ", pad))
				b.WriteString(cell)
			} else {
				b.WriteString(cell)
				if c < len(cells)-1 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	printRow(header)
	for _, row := range rows {
		printRow(row)
	}
}

func runeLen(s string) int {
	return len([]rune(s))
}
