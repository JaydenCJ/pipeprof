// Package splitcmd turns a pipeline string like
//
//	cat access.log | grep ERROR | sort | uniq -c
//
// into its stages without executing anything. Splitting is quote-aware
// (single quotes, double quotes, backslash escapes) and substitution-aware
// ($(…) and `…` never split), so a '|' inside an argument survives intact.
//
// Two parsing modes exist. In the default mode every stage is additionally
// parsed to an argv and executed directly — shell metacharacters that a
// direct exec cannot honor (redirection, globs, expansion, subshells) are
// rejected with an error that points at --shell. In shell mode the stage
// text is left untouched and later handed to `sh -c`, so the full shell
// feature set applies per stage while pipeprof still owns the pipes.
package splitcmd

import (
	"errors"
	"fmt"
	"strings"
)

// Stage is one pipeline segment. Argv is the parsed argument vector in
// direct-exec mode; it is nil in shell mode, in which case Text is run
// via `sh -c`.
type Stage struct {
	Text string
	Argv []string
}

// Parse splits a full pipeline string into stages. With shell=false each
// stage is parsed to an argv; with shell=true stages keep their raw text.
func Parse(pipeline string, shell bool) ([]Stage, error) {
	if strings.TrimSpace(pipeline) == "" {
		return nil, errors.New("empty pipeline")
	}
	raw, err := SplitStages(pipeline)
	if err != nil {
		return nil, err
	}
	stages := make([]Stage, len(raw))
	for i, text := range raw {
		stages[i] = Stage{Text: text}
		if shell {
			continue
		}
		argv, err := ParseArgv(text)
		if err != nil {
			return nil, fmt.Errorf("stage %d (%s): %w", i+1, text, err)
		}
		stages[i].Argv = argv
	}
	return stages, nil
}

// SplitStages splits a pipeline on top-level '|' only. Pipes inside single
// quotes, double quotes, backslash escapes, $(…) command substitution and
// `…` backticks do not split. Each returned stage is whitespace-trimmed
// and guaranteed non-empty.
func SplitStages(s string) ([]string, error) {
	var (
		parts    []string
		cur      strings.Builder
		inSingle bool
		inDouble bool
		inTick   bool
		depth    int // $( … ) nesting
	)
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			cur.WriteByte(c)
			i++
		case c == '\\':
			// Backslash escapes the next byte everywhere outside single
			// quotes; copy both so a quoted "\|" never splits.
			cur.WriteByte(c)
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			}
			cur.WriteByte(c)
			i++
		case inTick:
			if c == '`' {
				inTick = false
			}
			cur.WriteByte(c)
			i++
		case c == '\'':
			inSingle = true
			cur.WriteByte(c)
			i++
		case c == '"':
			inDouble = true
			cur.WriteByte(c)
			i++
		case c == '`':
			inTick = true
			cur.WriteByte(c)
			i++
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			depth++
			cur.WriteString("$(")
			i += 2
		case c == '(' && depth > 0:
			depth++
			cur.WriteByte(c)
			i++
		case c == ')' && depth > 0:
			depth--
			cur.WriteByte(c)
			i++
		case c == '|' && depth == 0:
			if i+1 < len(s) && s[i+1] == '|' {
				return nil, errors.New(`"||" is a shell control operator, not a pipe; pipeprof profiles a single pipeline`)
			}
			parts = append(parts, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	switch {
	case inSingle:
		return nil, errors.New("unterminated single quote")
	case inDouble:
		return nil, errors.New("unterminated double quote")
	case inTick:
		return nil, errors.New("unterminated backtick")
	case depth > 0:
		return nil, errors.New("unbalanced $( … )")
	}
	parts = append(parts, cur.String())

	out := make([]string, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("stage %d is empty (stray '|'?)", i+1)
		}
		out[i] = p
	}
	return out, nil
}

// ParseArgv parses one stage into an argument vector using POSIX-style
// quoting: single quotes are literal, double quotes group with backslash
// escapes for \" \\ \$ \`, and a bare backslash escapes the next byte.
// Anything a direct exec cannot honor is rejected with a hint at --shell —
// silently passing a literal '*' or '$HOME' to a program would be a lie.
func ParseArgv(stage string) ([]string, error) {
	var (
		argv    []string
		tok     strings.Builder
		started bool
	)
	flush := func() {
		if started {
			argv = append(argv, tok.String())
			tok.Reset()
			started = false
		}
	}
	i := 0
	for i < len(stage) {
		c := stage[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'':
			started = true
			end := strings.IndexByte(stage[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			tok.WriteString(stage[i+1 : i+1+end])
			i += end + 2
		case c == '"':
			started = true
			i++
			closed := false
			for i < len(stage) {
				d := stage[i]
				if d == '"' {
					closed = true
					i++
					break
				}
				if d == '\\' && i+1 < len(stage) {
					switch next := stage[i+1]; next {
					case '"', '\\', '$', '`':
						tok.WriteByte(next)
						i += 2
						continue
					}
					tok.WriteByte('\\')
					i++
					continue
				}
				if d == '$' {
					return nil, errors.New("expansion ($) inside double quotes is not performed here; use single quotes or rerun with --shell")
				}
				if d == '`' {
					return nil, errors.New("command substitution (`) is not performed here; rerun with --shell")
				}
				tok.WriteByte(d)
				i++
			}
			if !closed {
				return nil, errors.New("unterminated double quote")
			}
		case c == '\\':
			if i+1 >= len(stage) {
				return nil, errors.New("trailing backslash")
			}
			started = true
			tok.WriteByte(stage[i+1])
			i += 2
		default:
			if err := metaError(c); err != nil {
				return nil, err
			}
			started = true
			tok.WriteByte(c)
			i++
		}
	}
	flush()
	if len(argv) == 0 {
		return nil, errors.New("empty stage")
	}
	return argv, nil
}

// metaError rejects unquoted shell metacharacters that direct exec would
// pass through as literal bytes, which is never what the user meant.
func metaError(c byte) error {
	switch c {
	case '<', '>':
		return fmt.Errorf("redirection (%q) is not supported here; rerun with --shell", string(c))
	case ';', '&':
		return fmt.Errorf("control operator (%q) is not supported here; rerun with --shell", string(c))
	case '(', ')':
		return errors.New("subshells are not supported here; rerun with --shell")
	case '`':
		return errors.New("command substitution (`) is not performed here; rerun with --shell")
	case '$':
		return errors.New("expansion ($) is not performed here; quote it or rerun with --shell")
	case '*', '?', '[':
		return fmt.Errorf("glob character (%q) would not be expanded; quote it or rerun with --shell", string(c))
	}
	return nil
}
