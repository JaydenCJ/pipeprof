// Formatting helpers shared by the text renderer: human-readable byte
// sizes, durations, and grouped integers. Kept pure so every branch is
// unit-testable without running a pipeline.
package report

import (
	"fmt"
	"strconv"
	"time"
)

// HumanBytes renders a byte count in binary units (KiB = 1024 B).
// Negative counts mean "not measured" and render as an em dash.
func HumanBytes(n int64) string {
	if n < 0 {
		return "—"
	}
	if n < 1024 {
		return strconv.FormatInt(n, 10) + "B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, units[i])
	}
	return fmt.Sprintf("%.1f%s", v, units[i])
}

// HumanDuration renders a duration at the precision a profile table needs:
// sub-10ms with one decimal, milliseconds up to a second, seconds with two
// decimals, then minutes. Negative durations mean "never" → em dash.
func HumanDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "—"
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		m := int(d.Minutes())
		s := d.Seconds() - float64(m)*60
		return fmt.Sprintf("%dm%04.1fs", m, s)
	}
}

// Comma renders an integer with thousands separators (1234567 → 1,234,567).
func Comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return neg + s
	}
	var out []byte
	lead := len(s) % 3
	if lead > 0 {
		out = append(out, s[:lead]...)
	}
	for i := lead; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	return neg + string(out)
}

// Truncate shortens s to max runes, marking the cut with a single '…'.
func Truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
