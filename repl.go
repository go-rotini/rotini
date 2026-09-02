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
		echoErr: true,
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

// WithErrorEcho controls whether a failing command's error is written to the REPL's output
// (default true). Turn it off when the program's own funnel already reports to the same stream.
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
	ask := newAsker(r.in, r.out)

	for {
		select {
		case <-ctx.Done():
			return nil // a canceled REPL ended on request, not in failure
		default:
		}
		if r.prompt != "" && r.out != nil {
			fmt.Fprint(r.out, r.prompt)
		}

		line, err := ask.readLine(ctx)
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
