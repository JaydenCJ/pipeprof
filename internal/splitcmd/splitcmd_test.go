// Tests for pipeline splitting and argv parsing. Getting these wrong
// means profiling a different pipeline than the user typed — the worst
// failure mode this project has — so quoting and metacharacter edge
// cases get the densest coverage.
package splitcmd

import (
	"reflect"
	"strings"
	"testing"
)

func mustSplit(t *testing.T, s string) []string {
	t.Helper()
	parts, err := SplitStages(s)
	if err != nil {
		t.Fatalf("SplitStages(%q): %v", s, err)
	}
	return parts
}

func mustArgv(t *testing.T, s string) []string {
	t.Helper()
	argv, err := ParseArgv(s)
	if err != nil {
		t.Fatalf("ParseArgv(%q): %v", s, err)
	}
	return argv
}

func TestSplitBasicShapes(t *testing.T) {
	cases := map[string][]string{
		"cat access.log | grep ERROR | wc -l": {"cat access.log", "grep ERROR", "wc -l"},
		"sort|uniq":                           {"sort", "uniq"},
		"wc -l":                               {"wc -l"},
	}
	for in, want := range cases {
		if got := mustSplit(t, in); !reflect.DeepEqual(got, want) {
			t.Fatalf("SplitStages(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuotedOrEscapedPipesDoNotSplit(t *testing.T) {
	// A '|' inside quotes, escapes or backticks is data, not plumbing.
	cases := map[string]string{
		`grep 'a|b' | wc -l`:           `grep 'a|b'`,
		`awk "BEGIN{print 1|2}" | cat`: `awk "BEGIN{print 1|2}"`,
		`grep a\|b | wc -l`:            `grep a\|b`,
		"echo `ls | wc -l` | cat":      "echo `ls | wc -l`",
	}
	for in, first := range cases {
		got := mustSplit(t, in)
		if len(got) != 2 || got[0] != first {
			t.Fatalf("SplitStages(%q) = %q, want stage 1 %q of 2", in, got, first)
		}
	}
}

func TestPipeInsideCommandSubstitutionDoesNotSplit(t *testing.T) {
	// $(…) may itself contain a pipeline; only top-level pipes split,
	// even when the substitution nests.
	got := mustSplit(t, `echo $(ls | wc -l) | cat`)
	if len(got) != 2 || got[0] != `echo $(ls | wc -l)` {
		t.Fatalf("got %q", got)
	}
	got = mustSplit(t, `echo $(echo $(a | b) | c) | cat`)
	if len(got) != 2 {
		t.Fatalf("got %q, want nested $() kept whole", got)
	}
}

func TestDoublePipeIsRejected(t *testing.T) {
	if _, err := SplitStages("grep x || echo missing"); err == nil {
		t.Fatal(`"||" must be rejected, it is a control operator`)
	}
}

func TestEmptyStageIsRejected(t *testing.T) {
	for _, s := range []string{"a | | b", "| b", "a |", "a | b |"} {
		if _, err := SplitStages(s); err == nil {
			t.Fatalf("SplitStages(%q) accepted an empty stage", s)
		}
	}
}

func TestUnterminatedConstructsAreRejected(t *testing.T) {
	for _, s := range []string{`grep 'a | b`, `grep "a | b`, "echo `x | y", "echo $(a | b"} {
		if _, err := SplitStages(s); err == nil {
			t.Fatalf("SplitStages(%q) accepted an unterminated construct", s)
		}
	}
}

func TestArgvBasicWords(t *testing.T) {
	got := mustArgv(t, "grep -c ERROR")
	if !reflect.DeepEqual(got, []string{"grep", "-c", "ERROR"}) {
		t.Fatalf("got %q", got)
	}
}

func TestArgvSingleQuotesAreLiteral(t *testing.T) {
	got := mustArgv(t, `awk '{print $1}'`)
	if !reflect.DeepEqual(got, []string{"awk", "{print $1}"}) {
		t.Fatalf("got %q, want the $ kept literal inside single quotes", got)
	}
}

func TestArgvDoubleQuotesGroupAndUnescape(t *testing.T) {
	got := mustArgv(t, `grep "a b \" c\\d"`)
	if !reflect.DeepEqual(got, []string{"grep", `a b " c\d`}) {
		t.Fatalf("got %q", got)
	}
	// An escaped $ stays literal — only the unescaped form is rejected.
	got = mustArgv(t, `grep "\$HOME"`)
	if !reflect.DeepEqual(got, []string{"grep", "$HOME"}) {
		t.Fatalf("got %q, want the escaped $ kept literal", got)
	}
}

func TestArgvBackslashEscapesSpace(t *testing.T) {
	got := mustArgv(t, `cat my\ file.txt`)
	if !reflect.DeepEqual(got, []string{"cat", "my file.txt"}) {
		t.Fatalf("got %q", got)
	}
}

func TestArgvQuotedEdgeCases(t *testing.T) {
	// An empty quoted argument is a real argument; adjacent quoted
	// pieces concatenate into one word, POSIX-style.
	got := mustArgv(t, `grep '' file`)
	if !reflect.DeepEqual(got, []string{"grep", "", "file"}) {
		t.Fatalf("got %q, want the empty argument preserved", got)
	}
	got = mustArgv(t, `echo 'a'"b"c`)
	if !reflect.DeepEqual(got, []string{"echo", "abc"}) {
		t.Fatalf("got %q", got)
	}
}

func TestArgvRejectsMetacharactersWithShellHint(t *testing.T) {
	// Each of these would silently pass a literal byte to the program in
	// direct-exec mode — the error must instead point at --shell.
	cases := []string{
		"sort > out.txt",
		"wc -l < in.txt",
		"grep x; echo done",
		"grep x & true",
		"echo `date`",
		"grep $PATTERN",
		"cat *.log",
		"ls file?",
		"grep [ab]c",
		"(cd /tmp)",
	}
	for _, s := range cases {
		_, err := ParseArgv(s)
		if err == nil {
			t.Fatalf("ParseArgv(%q) accepted a shell metacharacter", s)
		}
		if !strings.Contains(err.Error(), "--shell") {
			t.Fatalf("ParseArgv(%q) error %q does not hint at --shell", s, err)
		}
	}
}

func TestArgvRejectsExpansionInsideDoubleQuotes(t *testing.T) {
	if _, err := ParseArgv(`grep "$HOME"`); err == nil {
		t.Fatal(`"$HOME" inside double quotes must be rejected, not passed literally`)
	}
}

func TestArgvRejectsTrailingBackslash(t *testing.T) {
	if _, err := ParseArgv(`echo a\`); err == nil {
		t.Fatal("trailing backslash must be rejected")
	}
}

func TestParseDirectModeFillsArgv(t *testing.T) {
	stages, err := Parse("tr a-z A-Z | wc -c", false)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("got %d stages", len(stages))
	}
	if !reflect.DeepEqual(stages[0].Argv, []string{"tr", "a-z", "A-Z"}) {
		t.Fatalf("stage 1 argv = %q", stages[0].Argv)
	}
	if stages[1].Text != "wc -c" {
		t.Fatalf("stage 2 text = %q", stages[1].Text)
	}
}

func TestParseShellModeKeepsRawTextAndAllowsRedirects(t *testing.T) {
	stages, err := Parse("wc -l < /dev/null | cat", true)
	if err != nil {
		t.Fatalf("Parse shell mode: %v", err)
	}
	if stages[0].Argv != nil {
		t.Fatalf("shell mode must not parse argv, got %q", stages[0].Argv)
	}
	if stages[0].Text != "wc -l < /dev/null" {
		t.Fatalf("stage text = %q", stages[0].Text)
	}
}

func TestParseErrorNamesTheFailingStage(t *testing.T) {
	_, err := Parse("cat ok.log | sort > out.txt", false)
	if err == nil {
		t.Fatal("expected an error for the redirect in stage 2")
	}
	if !strings.Contains(err.Error(), "stage 2") {
		t.Fatalf("error %q does not name stage 2", err)
	}
}

func TestParseEmptyPipelineIsRejected(t *testing.T) {
	for _, s := range []string{"", "   ", "\t"} {
		if _, err := Parse(s, false); err == nil {
			t.Fatalf("Parse(%q) accepted an empty pipeline", s)
		}
	}
}

func TestParsePreservesUnicodeArguments(t *testing.T) {
	stages, err := Parse(`grep '日本語' | wc -l`, false)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if stages[0].Argv[1] != "日本語" {
		t.Fatalf("argv = %q", stages[0].Argv)
	}
}
