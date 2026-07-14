// Package cli implements the pipeprof command-line interface.
//
// The entry point is Run, which takes argv and explicit streams and
// returns a process exit code. Keeping the CLI a pure function of its
// inputs (no os.Exit, no global state) is what lets the integration tests
// drive every flag in-process, deterministically.
//
// Exit codes:
//
//	0-255  the pipeline's own exit code (last stage, or the rightmost
//	       failing stage with --pipefail; 128+signal for signal deaths)
//	2      usage error — bad flags, unparsable pipeline, unreadable files
//	       (note: a stage can also legitimately exit 2, e.g. grep on error)
//	124    the pipeline was killed by --timeout
//	125    internal pipeprof error
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/JaydenCJ/pipeprof/internal/meter"
	"github.com/JaydenCJ/pipeprof/internal/pipeline"
	"github.com/JaydenCJ/pipeprof/internal/report"
	"github.com/JaydenCJ/pipeprof/internal/splitcmd"
	"github.com/JaydenCJ/pipeprof/internal/version"
)

const (
	exitUsage    = 2
	exitTimeout  = 124
	exitInternal = 125
)

const usageText = `pipeprof %s — profile every stage of a shell pipeline at once

Usage:
  pipeprof [flags] 'stage1 | stage2 | stage3'

The pipeline runs unmodified; pipeprof splices a counting tap onto every
boundary and prints a per-stage table (bytes, records, wall time, CPU
time, time to first output, exit code) to stderr. The pipeline's own
stdout passes through untouched, so pipeprof can sit inside a larger
pipe. Quote the whole pipeline as one argument.

Flags:
  --json            emit the report as JSON (schema_version 1)
  --report FILE     write the report to FILE instead of stderr
  --input FILE      feed FILE to the first stage (default: pipeprof stdin;
                    a terminal is never wired, stages then read EOF)
  --output FILE     write final-stage output to FILE instead of stdout
  --no-output       discard final-stage output (profile only)
  --records MODE    record counting: lines, nul, none (default lines)
  --shell           run each stage via 'sh -c' (enables globs, env vars,
                    redirections inside a stage)
  --pipefail        exit with the rightmost failing stage, like pipefail
  --timeout DUR     kill the whole pipeline after DUR (e.g. 30s); exit 124
  --wide            never truncate commands in the table
  --version         print the version and exit

Exit codes: the pipeline's exit code; 2 usage error; 124 timeout;
125 internal error.
`

// Run executes one pipeprof invocation and returns its exit code.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeprof", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		jsonOut     = fs.Bool("json", false, "emit JSON report")
		reportPath  = fs.String("report", "", "write report to file")
		inputPath   = fs.String("input", "", "feed file to first stage")
		outputPath  = fs.String("output", "", "write final output to file")
		noOutput    = fs.Bool("no-output", false, "discard final output")
		recordsMode = fs.String("records", "lines", "record counting mode")
		shellMode   = fs.Bool("shell", false, "run stages via sh -c")
		pipefail    = fs.Bool("pipefail", false, "rightmost failing exit code")
		timeout     = fs.Duration("timeout", 0, "kill pipeline after duration")
		wide        = fs.Bool("wide", false, "never truncate commands")
		showVersion = fs.Bool("version", false, "print version")
	)
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, usageText, version.Version)
			return 0
		}
		return usageError(stderr, err.Error())
	}

	args := fs.Args()
	if *showVersion || (len(args) == 1 && args[0] == "version") {
		fmt.Fprintf(stdout, "pipeprof %s\n", version.Version)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintf(stderr, usageText, version.Version)
		return exitUsage
	}
	if len(args) > 1 {
		return usageError(stderr, "expected exactly one pipeline argument; quote the whole pipeline: pipeprof 'a | b'")
	}

	mode, err := meter.ModeFromString(*recordsMode)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	if *outputPath != "" && *noOutput {
		return usageError(stderr, "--output and --no-output are mutually exclusive")
	}
	stages, err := splitcmd.Parse(args[0], *shellMode)
	if err != nil {
		return usageError(stderr, err.Error())
	}

	in, cleanupIn, err := resolveInput(*inputPath, stdin)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	defer cleanupIn()
	out, cleanupOut, err := resolveOutput(*outputPath, *noOutput, stdout)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	defer cleanupOut()
	reportW, cleanupReport, err := resolveReport(*reportPath, stderr)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	defer cleanupReport()

	res, err := pipeline.Run(pipeline.Options{
		Pipeline: args[0],
		Stages:   stages,
		Stdin:    in,
		Stdout:   out,
		Stderr:   stderr,
		Mode:     mode,
		Pipefail: *pipefail,
		Timeout:  *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pipeprof: %v\n", err)
		return exitInternal
	}

	if *jsonOut {
		if err := report.RenderJSON(reportW, res); err != nil {
			fmt.Fprintf(stderr, "pipeprof: writing report: %v\n", err)
			return exitInternal
		}
	} else {
		report.RenderText(reportW, res, *wide)
	}

	if res.TimedOut {
		return exitTimeout
	}
	return res.ExitCode
}

func usageError(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "pipeprof: %s\n", msg)
	fmt.Fprintln(stderr, "run 'pipeprof --help' for usage")
	return exitUsage
}

// resolveInput picks the first stage's stdin: an --input file, or the
// caller's stdin — except a terminal, which is never wired so that
// `pipeprof 'cat | …'` typed interactively cannot silently hang waiting
// for keystrokes.
func resolveInput(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot open --input: %v", err)
		}
		return f, func() { f.Close() }, nil
	}
	noop := func() {}
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return nil, noop, nil
		}
	}
	return stdin, noop, nil
}

func resolveOutput(path string, discard bool, stdout io.Writer) (io.Writer, func(), error) {
	noop := func() {}
	if discard {
		return nil, noop, nil
	}
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot create --output: %v", err)
		}
		return f, func() { f.Close() }, nil
	}
	return stdout, noop, nil
}

func resolveReport(path string, stderr io.Writer) (io.Writer, func(), error) {
	if path == "" {
		return stderr, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create --report: %v", err)
	}
	return f, func() { f.Close() }, nil
}
