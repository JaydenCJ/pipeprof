// Tests for the boundary tap. Record counting must be independent of how
// the stream happens to be chunked — a pipeline moves the same bytes in
// arbitrary read sizes, and the counts may never depend on that.
package meter

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeClock returns a deterministic, strictly increasing clock so
// first/last byte timestamps can be asserted exactly.
func fakeClock() func() time.Time {
	t := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Millisecond)
		return t
	}
}

func feed(t *testing.T, m *Meter, chunks ...string) Stats {
	t.Helper()
	for _, c := range chunks {
		m.Count([]byte(c))
	}
	return m.Stats()
}

func TestEmptyStreamCountsNothing(t *testing.T) {
	s := New(Lines).Stats()
	if s.Bytes != 0 || s.Records != 0 {
		t.Fatalf("empty meter: %+v", s)
	}
	if !s.FirstByte.IsZero() || !s.LastByte.IsZero() {
		t.Fatalf("empty meter must have zero timestamps: %+v", s)
	}
}

func TestBytesAccumulateAcrossChunks(t *testing.T) {
	s := feed(t, New(Lines), "abc", "defg", "h")
	if s.Bytes != 8 {
		t.Fatalf("bytes = %d, want 8", s.Bytes)
	}
}

func TestCompleteLinesAreCounted(t *testing.T) {
	s := feed(t, New(Lines), "one\ntwo\nthree\n")
	if s.Records != 3 {
		t.Fatalf("records = %d, want 3", s.Records)
	}
}

func TestTrailingPartialLineCountsAsARecord(t *testing.T) {
	// `printf 'a\nb'` produces two things a consumer sees; the meter
	// must agree with the consumer, not with the newline count.
	s := feed(t, New(Lines), "a\nb")
	if s.Records != 2 {
		t.Fatalf("records = %d, want 2 (trailing partial counts)", s.Records)
	}
}

func TestRecordSplitAcrossChunksCountsOnce(t *testing.T) {
	whole := feed(t, New(Lines), "alpha\nbeta\n")
	split := feed(t, New(Lines), "alp", "ha\nbe", "ta\n")
	if whole.Records != split.Records || whole.Bytes != split.Bytes {
		t.Fatalf("chunking changed counts: whole=%+v split=%+v", whole, split)
	}
}

func TestNulModeCountsNulDelimitedRecords(t *testing.T) {
	s := feed(t, New(Nul), "a\x00b\x00", "c")
	if s.Records != 3 {
		t.Fatalf("records = %d, want 3 (two complete + one partial)", s.Records)
	}
}

func TestNulModeIgnoresNewlines(t *testing.T) {
	// find -print0 output legitimately contains newlines inside names.
	s := feed(t, New(Nul), "dir/with\nnewline\x00plain\x00")
	if s.Records != 2 {
		t.Fatalf("records = %d, want 2", s.Records)
	}
}

func TestNoneModeReportsMinusOne(t *testing.T) {
	s := feed(t, New(None), "binary\x00data\nhere")
	if s.Records != -1 {
		t.Fatalf("records = %d, want -1 in Mode None", s.Records)
	}
	if s.Bytes != 16 {
		t.Fatalf("bytes = %d, want 16", s.Bytes)
	}
}

func TestFirstAndLastByteTimestamps(t *testing.T) {
	m := New(Lines)
	m.now = fakeClock()
	m.Count([]byte("a"))
	m.Count([]byte("b"))
	m.Count([]byte("c"))
	s := m.Stats()
	base := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	if !s.FirstByte.Equal(base.Add(1 * time.Millisecond)) {
		t.Fatalf("first byte at %v", s.FirstByte)
	}
	if !s.LastByte.Equal(base.Add(3 * time.Millisecond)) {
		t.Fatalf("last byte at %v", s.LastByte)
	}
}

func TestEmptyChunksDoNotDisturbCounts(t *testing.T) {
	m := New(Lines)
	m.Count(nil)
	m.Count([]byte{})
	m.Count([]byte("x\n"))
	m.Count([]byte{})
	s := m.Stats()
	if s.Bytes != 2 || s.Records != 1 {
		t.Fatalf("got %+v", s)
	}
}

func TestReaderWrapperCountsEverything(t *testing.T) {
	m := New(Lines)
	src := strings.NewReader("one\ntwo\nthree")
	var sink bytes.Buffer
	if _, err := io.Copy(&sink, m.Reader(src)); err != nil {
		t.Fatalf("copy: %v", err)
	}
	s := m.Stats()
	if s.Bytes != 13 || s.Records != 3 {
		t.Fatalf("got %+v, want 13 bytes / 3 records", s)
	}
	if sink.String() != "one\ntwo\nthree" {
		t.Fatalf("reader altered the data: %q", sink.String())
	}
}

func TestModeParsingRoundTripsAndRejectsUnknown(t *testing.T) {
	for _, name := range []string{"lines", "nul", "none"} {
		m, err := ModeFromString(name)
		if err != nil {
			t.Fatalf("ModeFromString(%q): %v", name, err)
		}
		if m.String() != name {
			t.Fatalf("round trip %q → %q", name, m.String())
		}
	}
	for _, bad := range []string{"words", "", "LINES"} {
		if _, err := ModeFromString(bad); err == nil {
			t.Fatalf("ModeFromString(%q) must be rejected", bad)
		}
	}
}

func TestLargeStreamExactCount(t *testing.T) {
	// 1 MiB in odd-sized chunks: totals must be exact, not approximate.
	m := New(Lines)
	line := strings.Repeat("x", 63) + "\n" // 64 bytes per record
	payload := strings.Repeat(line, 16384) // exactly 1 MiB
	for i := 0; i < len(payload); i += 7777 {
		end := i + 7777
		if end > len(payload) {
			end = len(payload)
		}
		m.Count([]byte(payload[i:end]))
	}
	s := m.Stats()
	if s.Bytes != 1<<20 {
		t.Fatalf("bytes = %d, want %d", s.Bytes, 1<<20)
	}
	if s.Records != 16384 {
		t.Fatalf("records = %d, want 16384", s.Records)
	}
}
