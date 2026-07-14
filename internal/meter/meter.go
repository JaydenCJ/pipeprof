// Package meter implements the counting taps pipeprof splices onto every
// pipeline boundary. A Meter observes the byte stream crossing one
// boundary and accumulates bytes, records (separator-delimited), and the
// timestamps of the first and last byte — everything the report needs,
// with no buffering and no interpretation of the data itself.
package meter

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"
)

// Mode selects how records are counted at a boundary.
type Mode int

const (
	// Lines counts newline-delimited records; a trailing partial line
	// still counts as one record, matching what a consumer would see.
	Lines Mode = iota
	// Nul counts NUL-delimited records (find -print0 / xargs -0 streams).
	Nul
	// None disables record counting; Stats.Records reports -1.
	None
)

// String returns the CLI spelling of the mode.
func (m Mode) String() string {
	switch m {
	case Lines:
		return "lines"
	case Nul:
		return "nul"
	case None:
		return "none"
	}
	return "unknown"
}

// ModeFromString parses the --records flag value.
func ModeFromString(s string) (Mode, error) {
	switch s {
	case "lines":
		return Lines, nil
	case "nul":
		return Nul, nil
	case "none":
		return None, nil
	}
	return Lines, fmt.Errorf("invalid records mode %q (valid: lines, nul, none)", s)
}

// Stats is a snapshot of everything a Meter has observed.
type Stats struct {
	Bytes     int64
	Records   int64 // -1 when the meter runs in Mode None
	FirstByte time.Time
	LastByte  time.Time
}

// Meter accumulates counts for one boundary. It is safe for one goroutine
// to feed it while another snapshots Stats.
type Meter struct {
	mu      sync.Mutex
	mode    Mode
	sep     byte
	now     func() time.Time
	bytes   int64
	seps    int64
	lastSep bool
	first   time.Time
	last    time.Time
}

// New returns a Meter counting records in the given mode.
func New(mode Mode) *Meter {
	m := &Meter{mode: mode, now: time.Now}
	switch mode {
	case Lines:
		m.sep = '\n'
	case Nul:
		m.sep = 0
	}
	return m
}

// Count feeds one observed chunk into the meter. Chunk boundaries are
// arbitrary; record counting only depends on separator bytes and the very
// last byte seen, so any split of the same stream counts identically.
func (m *Meter) Count(p []byte) {
	if len(p) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.now()
	if m.bytes == 0 {
		m.first = t
	}
	m.last = t
	m.bytes += int64(len(p))
	if m.mode == None {
		return
	}
	m.seps += int64(bytes.Count(p, []byte{m.sep}))
	m.lastSep = p[len(p)-1] == m.sep
}

// Stats snapshots the meter. Records is the number of complete records
// plus one for a trailing partial record, or -1 in Mode None.
func (m *Meter) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Stats{Bytes: m.bytes, Records: -1, FirstByte: m.first, LastByte: m.last}
	if m.mode != None {
		s.Records = m.seps
		if m.bytes > 0 && !m.lastSep {
			s.Records++
		}
	}
	return s
}

// Reader wraps r so every successful Read is counted by the meter. The
// pipeline pump counts writes explicitly instead, but Reader is the
// convenient form for callers that only observe one side of a stream.
func (m *Meter) Reader(r io.Reader) io.Reader {
	return &countingReader{r: r, m: m}
}

type countingReader struct {
	r io.Reader
	m *Meter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.m.Count(p[:n])
	}
	return n, err
}
