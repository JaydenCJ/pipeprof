// Command pipeprof profiles every stage of an unmodified shell pipeline.
// All behavior lives in internal/cli so it can be tested in-process; main
// only wires the real process streams and the exit code.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/JaydenCJ/pipeprof/internal/cli"
)

func main() {
	// When pipeprof itself sits inside a pipe and its reader exits early
	// (pipeprof … | head), the write to stdout must surface as an error
	// the report can mention — not as a silent SIGPIPE death that kills
	// the process before the stage table prints. Notify (rather than
	// Ignore) keeps the disposition local: exec'd stages still inherit
	// the default SIGPIPE behavior and die shell-style on a closed pipe.
	sigpipe := make(chan os.Signal, 1)
	signal.Notify(sigpipe, syscall.SIGPIPE)
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
