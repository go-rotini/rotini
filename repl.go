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
// # What rotini supplies, and what it does not
//
// rotini owns the half nobody else can: turning a line into a dispatched invocation against
// YOUR command tree. Resolving, tokenizing, a fresh Context per line, services that persist,
// a failure that does not end the session, interrupt and exit semantics — and
// [REPL.Complete], which no general-purpose line editor can offer because it does not know
// your commands.
//
// rotini does NOT own reading the line. History, arrow keys, ^R, multi-line input and
// bracketed paste are a solved problem with better libraries behind it — chzyer/readline,
// peterh/liner, c-bata/go-prompt — and they need raw terminal mode, which rotini deliberately
// does not take. The built-in reader is a plain byte-at-a-time line read: exactly right for a
// pipe, a test or a CI job, and deliberately minimal at a terminal.
//
// [REPL.WithLineReader] is the seam between the two. Everything else is configuration around
// it, so a program assembles the REPL it wants rather than accepting the one rotini imagined:
//
//   - [REPL.WithLineReader] — where a line comes from (readline, a socket, a test)
//   - [REPL.WithPromptFunc] — what the prompt says, per line, from live state
//   - [REPL.Complete] — what the command tree would complete, for that reader to render
//   - [REPL.WithInterrupts] — what ^C cancels
//   - [REPL.WithIntercept] — lines that never reach dispatch at all (\d, .schema, :q)
//
// The zero value is not usable; start from [NewREPL].
type REPL struct {
	program   *Program
	in        io.Reader
	out       io.Writer
	prompt    string
	promptFn  func() string
	readLine  func(ctx context.Context, prompt string) (string, error)
	interrupt <-chan struct{}
	intercept func(ctx context.Context, line string) (bool, error)
	exits     []string
	echoErr   bool
}

// ErrInterrupted reports that the user interrupted at the prompt — a ^C with a half-typed line,
// rather than an end of session.
//
// A [REPL.WithLineReader] returns it to say "discard this line and prompt again"; the session
// survives, which is what every interactive shell does and what the built-in loop could not
// express before. chzyer/readline's ErrInterrupt maps onto it directly.
//
// It is NOT how a session ends. That is end of input ([ErrNotInteractive] or io.EOF), an exit
// word, or the session context finishing.
var ErrInterrupted = errors.New("interrupted")

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
//
// [REPL.WithPromptFunc] overrides it when both are set.
func (r *REPL) WithPrompt(prompt string) *REPL { r.prompt = prompt; return r }

// WithPromptFunc computes the prompt before every read, so it can show session state the way
// a real shell does — the current database, a pending transaction, how deep a queue is:
//
//	repl.WithPromptFunc(func() string { return fmt.Sprintf("syncd(queue:%d)> ", queue.Len()) })
//
// It is called once per line, on the reading goroutine. A nil func restores [REPL.WithPrompt].
func (r *REPL) WithPromptFunc(fn func() string) *REPL { r.promptFn = fn; return r }

// WithLineReader replaces where a line comes from — the seam this whole type is built around.
//
// The built-in reader takes no dependency and does no editing, which is correct for a pipe and
// minimal at a terminal. Supplying one is how a program gets history, arrow keys, ^R and
// tab completion, from a library built for it:
//
//	rl, err := readline.New("")
//	repl.WithLineReader(func(_ context.Context, prompt string) (string, error) {
//		rl.SetPrompt(prompt)
//		line, err := rl.Readline()
//		switch {
//		case errors.Is(err, readline.ErrInterrupt):
//			return "", rotini.ErrInterrupted
//		case errors.Is(err, io.EOF):
//			return "", rotini.ErrNotInteractive
//		}
//		return line, err
//	})
//
// The contract is three errors and nothing else:
//
//   - [ErrInterrupted] — ^C at the prompt. The line is discarded and the loop prompts again.
//   - [ErrNotInteractive] or io.EOF — the input ended. The session closes cleanly, Run nil.
//   - anything else — a real I/O failure, returned by [REPL.Run].
//
// A reader that writes its own prompt should ignore the one it is handed. The REPL writes no
// prompt of its own once a reader is installed, since the two would print twice.
//
// A nil func restores the built-in reader.
func (r *REPL) WithLineReader(fn func(ctx context.Context, prompt string) (string, error)) *REPL {
	r.readLine = fn
	return r
}

// WithInterrupts makes a ^C cancel the RUNNING COMMAND instead of the session.
//
// This is the defect the seam exists to fix. Without it the REPL runs every line on the
// session's own context, so an interrupt that should abort one command tears down the shell —
// and in every interactive tool anyone has used (bash, python, psql, redis-cli) ^C returns you
// to the prompt. It is the most-pressed key in a REPL.
//
// **Send on a buffered channel, without blocking.** A receive cancels the line in flight;
// anything already pending when a line is submitted arrived at the PROMPT rather than at a
// command, and is dropped — a stray ^C at an empty prompt must not kill the next thing typed.
// The REPL reads the channel only while a command is running and never closes it.
//
//	sigint := make(chan os.Signal, 1)
//	signal.Notify(sigint, os.Interrupt)
//	interrupts := make(chan struct{}, 1)
//	go func() { for range sigint { select { case interrupts <- struct{}{}: default: } } }()
//	repl.WithInterrupts(interrupts)
//
// rotini wires no signal here on purpose: only the program knows whether its ^C means "abort
// this line" or "kill this process", and a framework guessing would be guessing about the
// user's most destructive key.
func (r *REPL) WithInterrupts(ch <-chan struct{}) *REPL { r.interrupt = ch; return r }

// WithIntercept installs a hook that sees each line BEFORE it is tokenized or dispatched,
// reporting whether it handled the line itself.
//
// It is the seam for meta-commands — the things a real REPL has that are not commands of the
// program: psql's \d, sqlite's .schema, a pager toggle, a session variable. They cannot be
// spec commands, because they mean nothing to the binary outside a session, and they cannot be
// handled by a line reader, because they need the program's state.
//
//	repl.WithIntercept(func(_ context.Context, line string) (bool, error) {
//		if !strings.HasPrefix(line, "\\") { return false, nil }
//		fmt.Fprintln(out, help[strings.TrimPrefix(line, "\\")])
//		return true, nil
//	})
//
// Returning true consumes the line. Returning an error ENDS the session — an interceptor that
// fails has lost track of its own state, which is not something to keep prompting through; a
// meta-command that merely failed should report that itself and return (true, nil).
//
// It sees every line, including blank ones and exit words, so a program can override either.
func (r *REPL) WithIntercept(fn func(ctx context.Context, line string) (bool, error)) *REPL {
	r.intercept = fn
	return r
}

// Complete returns what the command tree would complete for line, with the cursor at byte
// offset pos — sub-commands, flag names, enum values, and anything a [FlagValueCompleter] or
// [ArgValueCompleter] supplies dynamically.
//
// This is the one thing a REPL gets from rotini that no line editor can give it: the same
// completion the generated shell scripts use, from the same [Definition], against the same
// handlers. A general-purpose readline library cannot offer it because it does not know your
// commands.
//
//	rl, _ := readline.NewEx(&readline.Config{AutoComplete: completerFunc(repl.Complete)})
//
// A pos outside the line is clamped to its end. Candidates are bare names, ready to insert; a
// trailing space means the NEXT word is being completed, matching how a shell reads the same
// line. An empty result means "nothing to offer", which a reader should treat as leaving the
// line alone rather than as an error.
func (r *REPL) Complete(line string, pos int) []string {
	if r == nil || r.program == nil {
		return nil
	}
	if pos < 0 || pos > len(line) {
		pos = len(line)
	}
	head := line[:pos]

	words := splitArgs(head)
	// A trailing space means the word being completed is a new, empty one — "queue " offers
	// queue's sub-commands, where "queue" offers completions of the word "queue" itself.
	if head == "" || strings.HasSuffix(head, " ") || strings.HasSuffix(head, "\t") {
		words = append(words, "")
	}

	hits := complete(r.program.def, words, r.program.handlers, r.program.newRunContext())
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		// The shell protocol carries "name\tsummary"; a REPL inserts the name.
		name, _, _ := strings.Cut(hit, "\t")
		out = append(out, name)
	}
	return out
}

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

// Run reads and dispatches lines until an exit command, end of input, or a done context. It
// returns nil on a clean exit; a command's own failure never ends the loop and is never
// returned.
//
// Each line is dispatched on its OWN context, derived from ctx. That is what lets an interrupt
// from [REPL.WithInterrupts] cancel the command in flight and leave the session standing —
// while ctx finishing still ends everything, because that means the process is going down.
//
// Run may be called again on the same REPL, and the per-session state it keeps lives on the
// stack; configure before running, since the With methods are not synchronized.
func (r *REPL) Run(ctx context.Context) error {
	if r.program == nil {
		return InternalError(errors.New("rotini: repl has no program"))
	}
	read := r.readLine
	if read == nil {
		if r.in == nil {
			return UsageError(errors.New("rotini: repl has no input"))
		}
		builtin := newLineReader(r.in)
		read = func(ctx context.Context, _ string) (string, error) { return builtin.read(ctx) }
	}

	for {
		select {
		case <-ctx.Done():
			return nil // a canceled REPL ended on request, not in failure
		default:
		}

		prompt := r.promptText()
		// A supplied reader prints its own prompt; printing ours too would double it.
		if r.readLine == nil && prompt != "" && r.out != nil {
			fmt.Fprint(r.out, prompt)
		}

		line, err := read(ctx, prompt)
		switch classifyRead(err) {
		case readInterrupted:
			continue // ^C with a half-typed line: drop it, prompt again, session lives
		case readEnded:
			return nil // end of input, or a canceled session: both are clean exits
		case readFailed:
			return err
		}

		if r.intercept != nil {
			handled, err := r.intercept(ctx, line)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
		}

		argv := splitArgs(line)
		if len(argv) == 0 {
			continue
		}
		if r.isExit(argv[0]) {
			return nil
		}
		r.dispatch(ctx, argv)
	}
}

// dispatch runs one line on its own cancellable context, watching for an interrupt only while
// the command is actually running.
//
// The ordering is the whole design, and a background watcher got it wrong: it received an
// interrupt from the channel and THEN took a lock to find the line to cancel, so a ^C pressed
// at an empty prompt could win that race against the next line and kill a command the user
// typed afterwards. Found by `-race -count=2`, which is the only reason it did not ship.
//
// So the window is explicit instead. Anything already pending when a line is submitted arrived
// BEFORE the command existed and is dropped; only what arrives after it starts can cancel it.
func (r *REPL) dispatch(ctx context.Context, argv []string) {
	lineCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if r.interrupt != nil {
		// Drop a stale interrupt: the user pressed ^C at the prompt, not at this command.
		select {
		case <-r.interrupt:
		default:
		}
		go func() {
			select {
			case <-r.interrupt:
				cancel()
			case <-lineCtx.Done(): // the command finished on its own; stop watching
			}
		}()
	}

	// Each line dispatches under its own context — so an interrupt cancels the command in
	// flight and nothing else — without permanently changing how the program handles
	// signals, which Program.WithContext would.
	if _, err := r.program.RunContext(lineCtx, argv); err != nil && r.echoErr && r.out != nil {
		fmt.Fprintln(r.out, err.Error())
	}
}

// promptText is what this line's prompt should say.
func (r *REPL) promptText() string {
	if r.promptFn != nil {
		return r.promptFn()
	}
	return r.prompt
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
	readOK          readOutcome = iota
	readInterrupted             // ^C with a half-typed line: drop it and prompt again
	readEnded                   // end of input, or the context finished
	readFailed                  // a real I/O failure
)

func classifyRead(err error) readOutcome {
	switch {
	case err == nil:
		return readOK
	// Checked first: an interrupt is the one error that does NOT end the session, and a
	// reader may legitimately wrap it in something that also looks like a read failure.
	case errors.Is(err, ErrInterrupted):
		return readInterrupted
	case errors.Is(err, ErrNotInteractive),
		errors.Is(err, io.EOF),
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
		return "", fmt.Errorf("canceled: %w", ctx.Err())
	case r := <-ch:
		line := strings.TrimRight(r.line, "\r\n")
		if r.err != nil {
			if errors.Is(r.err, io.EOF) && line == "" {
				return "", ErrNotInteractive
			}
			if !errors.Is(r.err, io.EOF) {
				return "", fmt.Errorf("read line: %w", r.err)
			}
		}
		return line, nil
	}
}
