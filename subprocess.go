package rotini

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// [Subprocess]: running an external command with environment, working-directory and
// timeout control, streamed output, and a failure that quotes the child's stderr
// instead of "exit status 1".

// Stream identifies which of a subprocess's output streams a line came from.
type Stream int

// The two output streams a subprocess line can come from.
const (
	StreamStdout Stream = iota
	StreamStderr
)

// String renders the stream name.
func (s Stream) String() string {
	if s == StreamStderr {
		return "stderr"
	}
	return "stdout"
}

// Line is one line of a subprocess's output, tagged with the stream it came from.
type Line struct {
	Stream Stream
	Text   string
}

// SubprocessError reports that a subprocess could not be started, exited non-zero, or passed
// its deadline. ExitCode is the process's own status, -1 when it never ran or was killed. It
// is [ErrInternal] — a program that shells out owns the command it chose — and carries the
// underlying *exec.ExitError for errors.As.
type SubprocessError struct {
	Name     string
	Args     []string
	ExitCode int
	Stderr   string // captured stderr when the caller did not stream it, else ""
	Cause    error
}

// Error renders the failure with the command, its exit status, and the first line
// of whatever it wrote to stderr — rather than a bare "exit status 1".
func (e *SubprocessError) Error() string {
	var b strings.Builder
	b.WriteString(e.Name)
	if e.ExitCode >= 0 {
		fmt.Fprintf(&b, " exited %d", e.ExitCode)
	} else {
		b.WriteString(" failed to run")
	}
	if trimmed := strings.TrimSpace(e.Stderr); trimmed != "" {
		fmt.Fprintf(&b, ": %s", firstLine(trimmed))
	} else if e.Cause != nil {
		fmt.Fprintf(&b, ": %s", e.Cause)
	}
	return b.String()
}

// Unwrap exposes the cause and the ErrInternal sentinel, so both errors.As on an
// *exec.ExitError and CategoryOf reach through.
func (e *SubprocessError) Unwrap() []error { return []error{e.Cause, ErrInternal} }

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}

// Subprocess runs an external command with streamed output, environment and working-directory
// control, and a timeout — the [exec.Cmd] wrapper a CLI reaches for when it shells out, whose
// failures quote the child's stderr instead of "exit status 1".
//
// The zero value is not usable; start from [NewSubprocess].
type Subprocess struct {
	name    string
	args    []string
	dir     string
	env     []string
	inherit bool
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	timeout time.Duration
}

// NewSubprocess returns a subprocess that will run name with args. Nothing is
// executed until [Subprocess.Run], [Subprocess.Output] or [Subprocess.Lines].
func NewSubprocess(name string, args ...string) *Subprocess {
	return &Subprocess{name: name, args: args, inherit: true}
}

// WithDir sets the working directory (default: the parent's).
func (s *Subprocess) WithDir(dir string) *Subprocess { s.dir = dir; return s }

// WithEnv adds "KEY=VALUE" entries on top of the inherited environment.
func (s *Subprocess) WithEnv(entries ...string) *Subprocess {
	s.env = append(s.env, entries...)
	return s
}

// WithoutParentEnv drops the inherited environment, so the child sees only what
// [Subprocess.WithEnv] added.
func (s *Subprocess) WithoutParentEnv() *Subprocess { s.inherit = false; return s }

// WithStdin gives the child an input stream (default: no input, so a child that reads stdin sees
// EOF rather than blocking on the parent's terminal).
func (s *Subprocess) WithStdin(r io.Reader) *Subprocess { s.stdin = r; return s }

// WithStdout streams the child's stdout to w as it is produced.
func (s *Subprocess) WithStdout(w io.Writer) *Subprocess { s.stdout = w; return s }

// WithStderr streams the child's stderr to w as it is produced.
func (s *Subprocess) WithStderr(w io.Writer) *Subprocess { s.stderr = w; return s }

// WithTimeout kills the child if it has not exited within d. Zero — the default —
// means no deadline beyond the context's.
func (s *Subprocess) WithTimeout(d time.Duration) *Subprocess {
	if d > 0 {
		s.timeout = d
	}
	return s
}

// cmd builds the configured *exec.Cmd and the cancel func for any timeout.
func (s *Subprocess) cmd(ctx context.Context) (*exec.Cmd, context.CancelFunc) {
	cancel := context.CancelFunc(func() {})
	if s.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
	}
	cmd := exec.CommandContext(ctx, s.name, s.args...)
	cmd.Dir = s.dir
	switch {
	case s.inherit:
		cmd.Env = append(os.Environ(), s.env...)
	case len(s.env) > 0:
		cmd.Env = s.env
	default:
		cmd.Env = []string{}
	}
	cmd.Stdin = s.stdin
	return cmd, cancel
}

// Run executes the command and returns its exit code. A non-zero exit is both a code and a
// [*SubprocessError], so a caller can branch on the number or just check the error. Streams
// given no writer are captured, so a failure can quote the child's stderr.
func (s *Subprocess) Run(ctx context.Context) (int, error) {
	cmd, cancel := s.cmd(ctx)
	defer cancel()

	var captured bytes.Buffer
	cmd.Stdout = s.stdout
	cmd.Stderr = s.stderr
	if cmd.Stderr == nil {
		cmd.Stderr = &captured
	}

	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if err == nil {
		return code, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		code = -1
	}
	return code, &SubprocessError{
		Name: s.name, Args: s.args, ExitCode: code,
		Stderr: captured.String(), Cause: err,
	}
}

// Output runs the command and returns its stdout, trimmed of the trailing
// newline. Any [Subprocess.WithStdout] is ignored: Output IS the consumer.
func (s *Subprocess) Output(ctx context.Context) (string, error) {
	var out bytes.Buffer
	saved := s.stdout
	s.stdout = &out
	_, err := s.Run(ctx)
	s.stdout = saved
	return strings.TrimRight(out.String(), "\n"), err
}

// drain reads the rest of r and discards it, so a child is never left blocked writing
// to a pipe nobody reads — which would hang cmd.Wait. Used only once the stream is
// already known to be unusable, so the discarded bytes are of no value.
func drain(r io.Reader) {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return
	}
}

// reap waits for a child we deliberately killed, releasing its process entry. The status is
// meaningless — the kill was ours — so it is dropped rather than surfaced.
func reap(cmd *exec.Cmd) {
	if err := cmd.Wait(); err != nil {
		return
	}
}

// Lines runs the command and yields its output one line at a time, tagged with the stream it
// came from — an iterator rather than a pair of callbacks, so a caller can break out and
// errors arrive in the loop rather than in a closure that cannot return one.
//
//	for line, err := range proc.Lines(ctx) {
//	    if err != nil { return err }
//	    fmt.Fprintln(rtx.Stdout, line.Text)
//	}
//
// The final iteration carries the run's error (nil on success). Stopping early
// kills the child.
func (s *Subprocess) Lines(ctx context.Context) iter.Seq2[Line, error] {
	return func(yield func(Line, error) bool) {
		ctx, stop := context.WithCancel(ctx)
		defer stop()

		cmd, cancel := s.cmd(ctx)
		defer cancel()

		outPipe, err := cmd.StdoutPipe()
		if err != nil {
			yield(Line{}, &SubprocessError{Name: s.name, Args: s.args, ExitCode: -1, Cause: err})
			return
		}
		errPipe, err := cmd.StderrPipe()
		if err != nil {
			yield(Line{}, &SubprocessError{Name: s.name, Args: s.args, ExitCode: -1, Cause: err})
			return
		}
		if err := cmd.Start(); err != nil {
			yield(Line{}, &SubprocessError{Name: s.name, Args: s.args, ExitCode: -1, Cause: err})
			return
		}

		lines := make(chan Line)
		var wg sync.WaitGroup

		// Scan returns false for both end-of-stream and a read failure, so the error must be
		// checked after the loop: otherwise a line past the 1MB cap, or an I/O fault, would
		// silently truncate the output and the run would still look successful.
		var scanOnce sync.Once
		var scanErr error
		scan := func(r io.Reader, stream Stream) {
			defer wg.Done()
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				select {
				case lines <- Line{Stream: stream, Text: sc.Text()}:
				case <-ctx.Done():
					return
				}
			}
			if err := sc.Err(); err != nil {
				scanOnce.Do(func() { scanErr = err })
				// Ask the child to stop, then keep draining. Cancellation alone is not
				// enough: it kills the process rotini started, not the rest of a shell
				// pipeline, and cmd.Wait blocks until the pipe is read to EOF.
				stop()
				drain(r)
			}
		}
		wg.Add(2)
		go scan(outPipe, StreamStdout)
		go scan(errPipe, StreamStderr)
		go func() { wg.Wait(); close(lines) }()

		for line := range lines {
			if !yield(line, nil) {
				stop() // the consumer broke out: kill the child
				reap(cmd)
				return
			}
		}

		waitErr := cmd.Wait()

		// A scan failure outranks the wait error: the output is incomplete, and the
		// non-zero status is usually just the kill that failure triggered. Reporting
		// "signal: killed" would hide the reason.
		if scanErr != nil {
			yield(Line{}, &SubprocessError{
				Name: s.name, Args: s.args, ExitCode: cmd.ProcessState.ExitCode(), Cause: scanErr,
			})
			return
		}
		if waitErr != nil {
			code := cmd.ProcessState.ExitCode()
			if _, isExit := errors.AsType[*exec.ExitError](waitErr); !isExit {
				code = -1
			}
			yield(Line{}, &SubprocessError{Name: s.name, Args: s.args, ExitCode: code, Cause: waitErr})
		}
	}
}
