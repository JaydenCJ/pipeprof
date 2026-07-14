// JSON renderer: a stable machine-readable profile (schema_version 1).
// Field order and semantics are documented in docs/how-it-works.md; adding
// fields is backwards-compatible, changing them bumps the schema version.
package report

import (
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/JaydenCJ/pipeprof/internal/pipeline"
	"github.com/JaydenCJ/pipeprof/internal/version"
)

type jsonReport struct {
	Tool          string          `json:"tool"`
	Version       string          `json:"version"`
	SchemaVersion int             `json:"schema_version"`
	Pipeline      string          `json:"pipeline"`
	RecordsMode   string          `json:"records_mode"`
	Pipefail      bool            `json:"pipefail"`
	WallMS        float64         `json:"wall_ms"`
	ExitCode      int             `json:"exit_code"`
	TimedOut      bool            `json:"timed_out"`
	OutputError   string          `json:"output_error,omitempty"`
	Stages        []jsonStage     `json:"stages"`
	Bottleneck    *jsonBottleneck `json:"bottleneck"`
}

type jsonStage struct {
	Index         int      `json:"index"`
	Command       string   `json:"command"`
	Argv          []string `json:"argv,omitempty"`
	ExitCode      int      `json:"exit_code"`
	Signal        string   `json:"signal,omitempty"`
	StartError    string   `json:"start_error,omitempty"`
	WallMS        float64  `json:"wall_ms"`
	UserMS        float64  `json:"user_ms"`
	SysMS         float64  `json:"sys_ms"`
	MaxRSSKB      int64    `json:"max_rss_kb"`
	InBytes       int64    `json:"in_bytes"`
	InRecords     int64    `json:"in_records"`
	OutBytes      int64    `json:"out_bytes"`
	OutRecords    int64    `json:"out_records"`
	FirstOutputMS float64  `json:"first_output_ms"` // -1 = no output
	ThroughputBPS int64    `json:"throughput_bps"`  // out bytes / stage wall
}

type jsonBottleneck struct {
	Stage    int     `json:"stage"`
	Command  string  `json:"command"`
	CPUShare float64 `json:"cpu_share"`
}

// RenderJSON writes the machine-readable report for res to w.
func RenderJSON(w io.Writer, res *pipeline.Result) error {
	rep := jsonReport{
		Tool:          "pipeprof",
		Version:       version.Version,
		SchemaVersion: 1,
		Pipeline:      res.Pipeline,
		RecordsMode:   res.Mode.String(),
		Pipefail:      res.Pipefail,
		WallMS:        ms(res.Wall),
		ExitCode:      res.ExitCode,
		TimedOut:      res.TimedOut,
		OutputError:   res.OutputErr,
		Stages:        make([]jsonStage, 0, len(res.Stages)),
	}
	for _, st := range res.Stages {
		rep.Stages = append(rep.Stages, jsonStage{
			Index:         st.Index,
			Command:       st.Command,
			Argv:          st.Argv,
			ExitCode:      st.ExitCode,
			Signal:        st.Signal,
			StartError:    st.StartErr,
			WallMS:        ms(st.Wall),
			UserMS:        ms(st.User),
			SysMS:         ms(st.Sys),
			MaxRSSKB:      st.MaxRSSKB,
			InBytes:       st.In.Bytes,
			InRecords:     st.In.Records,
			OutBytes:      st.Out.Bytes,
			OutRecords:    st.Out.Records,
			FirstOutputMS: firstOutMS(st.FirstOut),
			ThroughputBPS: throughput(st.Out.Bytes, st.Wall),
		})
	}
	if i, share, ok := Bottleneck(res); ok {
		rep.Bottleneck = &jsonBottleneck{
			Stage:    res.Stages[i].Index,
			Command:  res.Stages[i].Command,
			CPUShare: math.Round(share*1000) / 1000,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// ms converts a duration to milliseconds with microsecond precision —
// enough for a profile, small enough to stay readable in the output.
func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000
}

func firstOutMS(d time.Duration) float64 {
	if d < 0 {
		return -1
	}
	return ms(d)
}

func throughput(bytes int64, wall time.Duration) int64 {
	if wall <= 0 || bytes <= 0 {
		return 0
	}
	return int64(float64(bytes) / wall.Seconds())
}
