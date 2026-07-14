// Package pipeline launches every stage of a parsed pipeline as its own
// process, wires the stages together through counting taps, and collects
// per-stage measurements: bytes, records, wall time, CPU time, peak RSS,
// exit codes and time to first output.
//
// The wiring is deliberately explicit: pipeprof owns one os.Pipe pair per
// boundary and pumps data across it in userspace, counting as it goes.
// That is the whole trick — the stages run completely unmodified, exactly
// as the shell would run them, while every byte still passes a tap.
//
// Failure semantics mirror the shell: when a downstream stage exits early
// the tap closes the upstream read end, so the upstream process dies from
// SIGPIPE on its next write, just as it would in `yes | head -3`.
package pipeline

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/JaydenCJ/pipeprof/internal/meter"
	"github.com/JaydenCJ/pipeprof/internal/splitcmd"
)

const pumpBufSize = 64 * 1024

// Options configures one profiled run.
type Options struct {
	// Pipeline is the original pipeline text, echoed into the Result.
	Pipeline string
	// Stages come from splitcmd.Parse. A stage with nil Argv runs via
	// `<Shell> -c <Text>`.
	Stages []splitcmd.Stage
	// Stdin feeds the first stage. nil means the first stage reads EOF
	// immediately (the exec default of /dev/null). If Stdin never returns
	// EOF and the first stage exits, the internal pump goroutine stays
	// blocked until Stdin unblocks; Run itself does not wait for it.
	Stdin io.Reader
	// Stdout receives the final stage's output. nil discards it.
	Stdout io.Writer
	// Stderr receives every stage's stderr. nil discards it. A non-file
	// writer is serialized internally so concurrent stages cannot race.
	Stderr io.Writer
	// Mode selects record counting at every boundary.
	Mode meter.Mode
	// Pipefail makes the pipeline exit code the rightmost failing stage
	// instead of the last stage, like `set -o pipefail`.
	Pipefail bool
	// Timeout, when positive, kills every stage after the duration.
	Timeout time.Duration
	// Shell is the interpreter for Argv-less stages; default "sh".
	Shell string
}

// StageResult holds every measurement for one stage.
type StageResult struct {
	Index    int      // 1-based position in the pipeline
	Command  string   // original stage text
	Argv     []string // parsed argv; nil in shell mode
	ExitCode int      // 128+signal when signaled; 127 when start failed
	Signal   string   // e.g. "SIGPIPE" when the stage died from a signal
	StartErr string   // non-empty when the process could not start
	Started  time.Time
	Wall     time.Duration // Start → Wait for this stage
	User     time.Duration // CPU time in user mode
	Sys      time.Duration // CPU time in kernel mode
	MaxRSSKB int64         // peak resident set size, KiB
	In       meter.Stats   // what crossed the boundary into this stage
	Out      meter.Stats   // what crossed the boundary out of this stage
	FirstOut time.Duration // pipeline start → first output byte; -1 = none
}

// Result is the full profile of one pipeline run.
type Result struct {
	Pipeline  string
	Stages    []StageResult
	Wall      time.Duration
	ExitCode  int
	Pipefail  bool
	Mode      meter.Mode
	HasStdin  bool
	TimedOut  bool
	OutputErr string // non-empty when writing final output failed
}

// Run executes the pipeline and blocks until every stage has exited and
// every inner tap has drained. The returned error covers setup problems
// only; stage failures are reported inside the Result.
func Run(opts Options) (*Result, error) {
	n := len(opts.Stages)
	if n == 0 {
		return nil, errors.New("pipeline has no stages")
	}
	shell := opts.Shell
	if shell == "" {
		shell = "sh"
	}
	var stdout io.Writer = io.Discard
	if opts.Stdout != nil {
		stdout = opts.Stdout
	}
	stderr := stageStderr(opts.Stderr)

	results := make([]StageResult, n)
	cmds := make([]*exec.Cmd, n)
	for i, st := range opts.Stages {
		results[i] = StageResult{Index: i + 1, Command: st.Text, Argv: st.Argv, FirstOut: -1}
		if st.Argv != nil {
			cmds[i] = exec.Command(st.Argv[0], st.Argv[1:]...)
		} else {
			cmds[i] = exec.Command(shell, "-c", st.Text)
		}
		cmds[i].Stderr = stderr
	}

	// meters[i] taps the boundary in front of stage i; meters[n] taps the
	// final output. Stage i therefore reports In=meters[i], Out=meters[i+1].
	meters := make([]*meter.Meter, n+1)
	for i := range meters {
		meters[i] = meter.New(opts.Mode)
	}

	plumbing, err := wire(cmds, opts.Stdin != nil)
	if err != nil {
		return nil, err
	}

	// Start every stage. Parent-side duplicates of child fds are closed
	// immediately so EOF and EPIPE propagate the moment a stage exits.
	// A failed start still closes its ends, which makes the neighbors see
	// an instant EOF/EPIPE — the pipeline degrades instead of hanging.
	start := time.Now()
	for i := range cmds {
		results[i].Started = time.Now()
		if err := cmds[i].Start(); err != nil {
			results[i].StartErr = err.Error()
			results[i].ExitCode = 127
		}
		for _, f := range plumbing.childEnds[i] {
			f.Close()
		}
	}

	var (
		pumps     sync.WaitGroup
		outputErr string
	)
	// Boundary 0: external stdin → first stage. Not tracked by the wait
	// group — a stdin that never returns EOF must not wedge Run once the
	// stages themselves are done (see Options.Stdin).
	if plumbing.stdinW != nil {
		go pump(opts.Stdin, plumbing.stdinW, meters[0], nil, plumbing.stdinW.Close, nil)
	}
	// Inner boundaries: stage b-1 stdout → stage b stdin.
	for b := 1; b < n; b++ {
		up, down := plumbing.innerR[b], plumbing.innerW[b]
		pumps.Add(1)
		go func() {
			defer pumps.Done()
			pump(up, down, meters[b], up.Close, down.Close, nil)
		}()
	}
	// Boundary n: last stage stdout → external output.
	pumps.Add(1)
	go func() {
		defer pumps.Done()
		pump(plumbing.outR, stdout, meters[n], plumbing.outR.Close, nil, func(err error) {
			outputErr = err.Error()
		})
	}()

	var procs sync.WaitGroup
	for i := range cmds {
		if results[i].StartErr != "" {
			continue
		}
		procs.Add(1)
		go func(i int) {
			defer procs.Done()
			waitErr := cmds[i].Wait()
			results[i].Wall = time.Since(results[i].Started)
			fillProcState(&results[i], cmds[i].ProcessState, waitErr)
		}(i)
	}

	var timedOut atomic.Bool
	var timer *time.Timer
	if opts.Timeout > 0 {
		timer = time.AfterFunc(opts.Timeout, func() {
			timedOut.Store(true)
			for i, c := range cmds {
				if results[i].StartErr == "" && c.Process != nil {
					_ = c.Process.Kill()
				}
			}
		})
	}

	procs.Wait()
	if timer != nil {
		timer.Stop()
	}
	// Inner and output pumps always terminate once the stages have exited
	// (their upstream pipes hit EOF); waiting for them guarantees the
	// meters and the final output are complete before the report renders.
	pumps.Wait()
	wall := time.Since(start)

	for i := range results {
		results[i].In = meters[i].Stats()
		results[i].Out = meters[i+1].Stats()
		if fb := results[i].Out.FirstByte; !fb.IsZero() {
			results[i].FirstOut = fb.Sub(start)
		}
	}

	res := &Result{
		Pipeline:  opts.Pipeline,
		Stages:    results,
		Wall:      wall,
		Pipefail:  opts.Pipefail,
		Mode:      opts.Mode,
		HasStdin:  opts.Stdin != nil,
		TimedOut:  timedOut.Load(),
		OutputErr: outputErr,
		ExitCode:  aggregateExit(results, opts.Pipefail),
	}
	return res, nil
}

// plumb holds every pipe end the parent keeps or must close after Start.
type plumb struct {
	stdinW    *os.File     // write end feeding stage 0 stdin (nil without stdin)
	innerR    []*os.File   // innerR[b]: read end of stage b-1's stdout
	innerW    []*os.File   // innerW[b]: write end of stage b's stdin
	outR      *os.File     // read end of the last stage's stdout
	childEnds [][]*os.File // per-stage fds the child owns; parent closes post-Start
}

// wire creates one os.Pipe pair per boundary and attaches the child-side
// ends to the commands. On any pipe failure it closes everything it
// opened and returns the error.
func wire(cmds []*exec.Cmd, hasStdin bool) (*plumb, error) {
	n := len(cmds)
	p := &plumb{
		innerR:    make([]*os.File, n),
		innerW:    make([]*os.File, n),
		childEnds: make([][]*os.File, n),
	}
	var opened []*os.File
	mkpipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err != nil {
			for _, f := range opened {
				f.Close()
			}
			return nil, nil, err
		}
		opened = append(opened, r, w)
		return r, w, nil
	}

	if hasStdin {
		r, w, err := mkpipe()
		if err != nil {
			return nil, err
		}
		cmds[0].Stdin = r
		p.stdinW = w
		p.childEnds[0] = append(p.childEnds[0], r)
	}
	for b := 1; b < n; b++ {
		upR, upW, err := mkpipe()
		if err != nil {
			return nil, err
		}
		dnR, dnW, err := mkpipe()
		if err != nil {
			return nil, err
		}
		cmds[b-1].Stdout = upW
		cmds[b].Stdin = dnR
		p.innerR[b], p.innerW[b] = upR, dnW
		p.childEnds[b-1] = append(p.childEnds[b-1], upW)
		p.childEnds[b] = append(p.childEnds[b], dnR)
	}
	outR, outW, err := mkpipe()
	if err != nil {
		return nil, err
	}
	cmds[n-1].Stdout = outW
	p.outR = outR
	p.childEnds[n-1] = append(p.childEnds[n-1], outW)
	return p, nil
}

// pump moves bytes across one boundary, counting each chunk only after it
// was accepted downstream — the meter reports what actually crossed. On a
// write error (downstream exited) it closes the upstream read end so the
// upstream process gets SIGPIPE, shell-style, then stops.
func pump(r io.Reader, w io.Writer, m *meter.Meter, closeR, closeW func() error, onWriteErr func(error)) {
	closeBoth := func() {
		if closeR != nil {
			_ = closeR()
		}
		if closeW != nil {
			_ = closeW()
		}
	}
	buf := make([]byte, pumpBufSize)
	for {
		nr, rerr := r.Read(buf)
		if nr > 0 {
			if _, werr := w.Write(buf[:nr]); werr != nil {
				if onWriteErr != nil {
					onWriteErr(werr)
				}
				closeBoth()
				return
			}
			m.Count(buf[:nr])
		}
		if rerr != nil {
			closeBoth()
			return
		}
	}
}

// fillProcState extracts exit code, signal, CPU times and peak RSS.
func fillProcState(r *StageResult, ps *os.ProcessState, waitErr error) {
	if ps == nil {
		if waitErr != nil {
			r.StartErr = waitErr.Error()
			r.ExitCode = 127
		}
		return
	}
	r.User = ps.UserTime()
	r.Sys = ps.SystemTime()
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru != nil {
		r.MaxRSSKB = maxRSSKB(int64(ru.Maxrss))
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		r.Signal = signalName(ws.Signal())
		r.ExitCode = 128 + int(ws.Signal())
		return
	}
	r.ExitCode = ps.ExitCode()
}

// maxRSSKB normalizes getrusage's ru_maxrss to KiB: Linux reports KiB,
// macOS reports bytes.
func maxRSSKB(v int64) int64 {
	if runtime.GOOS == "darwin" {
		return v / 1024
	}
	return v
}

// signalName renders the common POSIX signals by name; anything exotic
// falls back to its number.
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return "SIG" + itoa(int(sig))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// aggregateExit computes the pipeline exit code: the last stage's, or the
// rightmost failing stage's with pipefail (like `set -o pipefail`).
func aggregateExit(results []StageResult, pipefail bool) int {
	exit := results[len(results)-1].ExitCode
	if !pipefail {
		return exit
	}
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].ExitCode != 0 {
			return results[i].ExitCode
		}
	}
	return 0
}

// stageStderr adapts the caller's stderr for concurrent stages: real files
// are shared directly (the kernel serializes), anything else is wrapped in
// a mutex so two stages cannot interleave inside one Write.
func stageStderr(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	if f, ok := w.(*os.File); ok {
		return f
	}
	return &syncWriter{w: w}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
