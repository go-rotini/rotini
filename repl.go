package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// REPL runs a [Program] as an interactive read-eval-print loop: each line is tokenized like a
// shell command line and dispatched against the same [Definition] the binary uses, so every
// command, flag and handler behaves exactly as it does from the shell.
//
// It rests on [Program.Run] being re-entrant: each line gets a fresh [Context], so one
// command's outcomes and exit code never leak into the next, and services bound with
// [Program.Bind] are seeded into every line.
//
// A REPL never exits the process — a non-zero command is reported and the loop continues. It
// ends at an exit command, at end of input, or when the context is done.
//
// The zero value is not usable; start from [NewREPL].
type REPL struct {
	program *Program
	in      io.Reader
	out     io.Writer
	prompt  string
	exits   []string
	echoErr bool
}

// NewREPL returns a loop dispatching to program. Input and output default to the
// program's own streams, so a REPL inherits whatever [Program.WithStdin] and
// friends configured (including a test's buffers).
func NewREPL(program *Program) *REPL {
	r := &REPL{
		program: program,
		prompt:  "> ",
		exits:   []string{"exit", "quit"},
	}
	if program != nil {
		r.in, r.out = program.stdin, program.stdout
	}
	return r
}

// WithPrompt sets the text written before each read (default "> "). An empty
// prompt writes nothing, which suits a piped session.
func (r *REPL) WithPrompt(prompt string) *REPL { r.prompt = prompt; return r }

// WithInput overrides the line source.
func (r *REPL) WithInput(in io.Reader) *REPL { r.in = in; return r }

// WithOutput overrides where the prompt and loop diagnostics are written. It does
// NOT redirect command output, which goes to the program's own streams.
func (r *REPL) WithOutput(out io.Writer) *REPL { r.out = out; return r }

// WithExitCommands replaces the words that end the loop (default exit, quit).
// Passing none leaves end-of-input and context cancellation as the only exits.
func (r *REPL) WithExitCommands(words ...string) *REPL { r.exits = words; return r }

// WithErrorEcho controls whether a failing command's error is written to the REPL's output.
//
// It is OFF by default, because a rotini program already reports its own failures: the default
// funnel prints every recorded error, and a custom funnel almost always does too. With the echo
// on as well, every error in a session appeared twice — once from the funnel on stderr, once
// from the REPL on stdout — which in a terminal is the same destination:
//
//	syncd> Error: unknown command "nosuchcommand" for "syncd"
//	       unknown command "nosuchcommand" for "syncd"
//
// Turn it on for a program whose funnel is deliberately silent, or one whose funnel writes
// somewhere the person at the prompt cannot see.
func (r *REPL) WithErrorEcho(enabled bool) *REPL { r.echoErr = enabled; return r }

// Run reads and dispatches lines until an exit command, end of input, or a done
// context. It returns nil on a clean exit; a command's own failure never ends the
// loop and is never returned.
func (r *REPL) Run(ctx context.Context) error {
	if r.program == nil {
		return InternalError(errors.New("rotini: repl has no program"))
	}
	if r.in == nil {
		return UsageError(errors.New("rotini: repl has no input"))
	}
	lines := newLineReader(r.in)

	for {
		select {
		case <-ctx.Done():
			return nil // a canceled REPL ended on request, not in failure
		default:
		}
		if r.prompt != "" && r.out != nil {
			fmt.Fprint(r.out, r.prompt)
		}

		line, err := lines.read(ctx)
		switch classifyRead(err) {
		case readEnded:
			return nil // end of input, or a canceled session: both are clean exits
		case readFailed:
			return err
		}

		argv := splitArgs(line)
		if len(argv) == 0 {
			continue
		}
		if r.isExit(argv[0]) {
			return nil
		}
		// Each line dispatches under the REPL's context — so canceling the session
		// cancels the command in flight — without permanently changing how the
		// program handles signals, which Program.WithContext would.
		if _, runErr := r.program.RunContext(ctx, argv); runErr != nil && r.echoErr && r.out != nil {
			fmt.Fprintln(r.out, runErr.Error())
		}
	}
}

func (r *REPL) isExit(word string) bool {
	for _, e := range r.exits {
		if strings.EqualFold(word, e) {
			return true
		}
	}
	return false
}

// readOutcome classifies a line read: an ended session is not a failure, and
// separating the two keeps "return nil" out of an error branch.
type readOutcome int

const (
	readOK     readOutcome = iota
	readEnded              // end of input, or the context finished
	readFailed             // a real I/O failure
)

func classifyRead(err error) readOutcome {
	switch {
	case err == nil:
		return readOK
	case errors.Is(err, ErrNotInteractive),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return readEnded
	default:
		return readFailed
	}
}

// splitArgs tokenizes a command line the way a shell does for the cases a REPL meets:
// whitespace separates, quotes group, and a backslash escapes the next character. Expansion,
// globbing and pipelines are deliberately out of scope.
func splitArgs(line string) []string {
	var (
		args    []string
		current strings.Builder
		quote   rune
		escaped bool
		started bool
	)
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			started = true
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	flush()
	return args
}

// ── reading a line without stealing the next one ─────────────────────────────.
//
// A REPL cannot use bufio.Scanner, and the reason is worth keeping written down. A buffered
// reader created per read fills its buffer from the stream and then goes out of scope, taking
// whatever it read past the newline with it. On a terminal that is invisible, because a tty
// hands over one line at a time and there is never a surplus to lose. Everywhere else — a pipe,
// a test, a CI job — the second line of a scripted session would vanish.
//
// So lineReader consumes exactly the bytes of the line it returns. An already-buffered source
// (a *bufio.Reader, strings.Reader or bytes.Buffer) is used as-is, keeping its buffering;
// anything else is read one byte at a time, which costs a syscall per character typed and is
// the price of not eating input that belongs to the next command — or to a subprocess one of
// them starts.
type lineReader struct{ in io.ByteReader }

func newLineReader(in io.Reader) lineReader {
	if br, ok := in.(io.ByteReader); ok {
		return lineReader{in: br}
	}
	return lineReader{in: byteAtATime{in}}
}

// byteAtATime adapts an io.Reader to io.ByteReader without reading ahead.
type byteAtATime struct{ r io.Reader }

func (b byteAtATime) ReadByte() (byte, error) {
	if b.r == nil {
		return 0, io.EOF
	}
	var buf [1]byte
	for {
		n, err := b.r.Read(buf[:])
		if n > 0 {
			return buf[0], nil
		}
		if err != nil {
			return 0, err
		}
		// n == 0 with a nil error is legal and means nothing yet; try again.
	}
}

// read reads one line, honoring ctx: the read runs on its own goroutine so a canceled context
// returns immediately rather than waiting for a keystroke that may never come. The goroutine
// may outlive the call, since a blocked Read cannot be interrupted; it writes to a buffered
// channel and exits on its own.
//
// End of input with nothing typed is [ErrNotInteractive], which classifyRead treats as a clean
// end of session rather than a failure.
func (l lineReader) read(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		var err error
		for {
			c, readErr := l.in.ReadByte()
			if readErr != nil {
				err = readErr
				break
			}
			if c == '\n' {
				break
			}
			b.WriteByte(c)
		}
		ch <- result{b.String(), err}
	}()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("rotini: repl canceled: %w", ctx.Err())
	case r := <-ch:
		line := strings.TrimRight(r.line, "\r\n")
		if r.err != nil {
			if errors.Is(r.err, io.EOF) && line == "" {
				return "", ErrNotInteractive
			}
			if !errors.Is(r.err, io.EOF) {
				return "", fmt.Errorf("rotini: read line: %w", r.err)
			}
		}
		return line, nil
	}
}
