package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Asking questions: [Prompt] (free text), [Confirm] (yes/no) and [Select] (a menu), each over
// an [io.Reader] rather than the terminal directly, so they behave identically interactively,
// from a pipe, and from a test. Input that ends without an answer is [ErrNotInteractive]
// rather than a hang.

// ErrNotInteractive reports that a prompt's input reached EOF without a line. It is what makes
// an interactive battery safe in a pipeline or CI job: the run fails fast and says why instead
// of blocking on a stdin nobody is typing into. It is an [ErrUsage] — the environment, not the
// program, is wrong.
var ErrNotInteractive = UsageError(errors.New("rotini: no input available (not interactive)"))

// ErrPromptInvalid reports that an answer failed validation and no retries
// remained. The underlying validation error is reachable with [errors.As].
var ErrPromptInvalid = UsageError(errors.New("rotini: invalid answer"))

// defaultRetries is how many times Confirm and Select re-ask an unrecognized
// answer. It is applied in their constructors, not at use, so WithRetries(0)
// means "one attempt" rather than colliding with the unset zero value.
const defaultRetries = 2

// asker is the reader/writer pair and cancellable line read shared by Prompt,
// Confirm and Select.
//
// It holds an io.ByteReader rather than a *bufio.Reader, and that is not a detail. Each
// Prompt/Confirm/Select is constructed separately, so wrapping the stream in a fresh
// bufio.Reader gave each one a PRIVATE buffer: the first read filled it with everything
// available — the answers to every later question — returned one line, and dropped the rest
// when it went out of scope. The next component saw EOF.
//
// A terminal hides it completely, because a tty hands over one line at a time and there is
// never a surplus to lose. Everywhere else — a pipe, a test, CI, a [Wizard] running rotini's
// own prompts — asking a second question was impossible.
//
// So an asker consumes exactly the bytes of the line it returns. A reader that is already
// buffered (a *bufio.Reader, strings.Reader, bytes.Buffer) is used as-is, which keeps its
// buffering and lets several askers share it; anything else is read one byte at a time, which
// costs a syscall per character of a human's answer and is the price of not stealing input
// that belongs to the next question — or to a subprocess.
type asker struct {
	in  io.ByteReader
	out io.Writer
	// src is the reader as handed in, before any byte-at-a-time adaptation. It exists so a
	// secret prompt can find the *os.File underneath and silence the right terminal —
	// unwrapping by type assertion would only work for the adapters rotini happens to use
	// today.
	src io.Reader
}

func newAsker(in io.Reader, out io.Writer) asker {
	if br, ok := in.(io.ByteReader); ok {
		return asker{in: br, out: out, src: in}
	}
	return asker{in: byteAtATime{in}, out: out, src: in}
}

// inputFile returns the terminal the user types at, or nil when the input is not a file.
func (a asker) inputFile() *os.File {
	f, _ := a.src.(*os.File)
	return f
}

// byteAtATime adapts an io.Reader to io.ByteReader without buffering ahead.
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

// readLineFrom accumulates bytes up to and including the first newline, returning the line
// without its terminator. A stream ending without one yields what it had, with io.EOF.
func readLineFrom(in io.ByteReader) (string, error) {
	var b strings.Builder
	for {
		c, err := in.ReadByte()
		if err != nil {
			return b.String(), err
		}
		if c == '\n' {
			return b.String(), nil
		}
		b.WriteByte(c)
	}
}

// readLine reads one line, honoring ctx: the read runs on its own goroutine so a canceled
// context returns immediately rather than waiting for a keystroke that may never come. The
// goroutine may outlive the call, since a blocked Read cannot be interrupted; it writes to a
// buffered channel and exits on its own.
func (a asker) readLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := readLineFrom(a.in)
		ch <- result{line, err}
	}()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("rotini: prompt canceled: %w", ctx.Err())
	case r := <-ch:
		line := strings.TrimRight(r.line, "\r\n")
		if r.err != nil {
			if errors.Is(r.err, io.EOF) && line == "" {
				return "", ErrNotInteractive
			}
			if !errors.Is(r.err, io.EOF) {
				return "", fmt.Errorf("rotini: read answer: %w", r.err)
			}
		}
		return line, nil
	}
}

func (a asker) write(s string) {
	if a.out != nil && s != "" {
		fmt.Fprint(a.out, s)
	}
}

// Prompt asks for one line of free text. The zero value is not usable; start from [NewPrompt].
type Prompt struct {
	asker

	label    string
	def      string
	validate func(string) error
	retries  int
	secret   bool
	mask     rune
}

// NewPrompt returns a prompt reading from in and writing its label to out. Pass a
// handler's [Context.Stdin] and [Context.Stdout] so the program's stream
// configuration applies.
func NewPrompt(in io.Reader, out io.Writer) *Prompt {
	return &Prompt{asker: newAsker(in, out)}
}

// WithLabel sets the text written before reading.
func (p *Prompt) WithLabel(label string) *Prompt { p.label = label; return p }

// WithDefault sets the value an empty answer resolves to, shown in the label as "[default]".
// A prompt with a default never reports [ErrNotInteractive] — the default is the
// non-interactive answer.
func (p *Prompt) WithDefault(value string) *Prompt { p.def = value; return p }

// WithValidate rejects an answer when fn returns an error; the message is shown
// and the question re-asked, up to the retry budget.
func (p *Prompt) WithValidate(fn func(string) error) *Prompt { p.validate = fn; return p }

// WithRetries sets how many times an invalid answer may be re-asked (default 0 —
// one attempt).
func (p *Prompt) WithRetries(n int) *Prompt {
	if n >= 0 {
		p.retries = n
	}
	return p
}

// Ask writes the label and reads one answer, applying the default and validation.
func (p *Prompt) Ask(ctx context.Context) (string, error) {
	for attempt := 0; ; attempt++ {
		p.write(promptLabel(p.label, p.def))
		answer, err := p.readAnswer(ctx)
		if errors.Is(err, ErrNotInteractive) && p.def != "" {
			return p.def, nil // a default makes the question answerable without a human
		}
		if err != nil {
			return "", err
		}
		if answer == "" {
			answer = p.def
		}
		if p.validate == nil {
			return answer, nil
		}
		if verr := p.validate(answer); verr != nil {
			if attempt >= p.retries {
				return "", fmt.Errorf("%w: %w", ErrPromptInvalid, verr)
			}
			p.write(verr.Error() + "\n")
			continue
		}
		return answer, nil
	}
}

// WithSecret reads the answer without echoing it, for a password, token or passphrase.
//
// Echo is disabled on the terminal for the duration of the read and restored afterwards on every
// path, including a panic and a canceled context. A newline is written when the read ends, since
// the user's own Enter was not echoed either and without it the next output lands on the prompt.
//
// # Why this belongs to rotini
//
// Doing it by hand means putting the terminal in a non-default state and being certain to undo
// it — and the failure mode is not a wrong value, it is a SHELL LEFT WITH ECHO OFF, which
// survives the process and confuses the user's next command. rotini installs the signal trap
// (see [Program.WithSignals]), so a SIGINT during the read cancels the run context, the read
// returns, and the restore runs. That is the part a program cannot reliably do for itself
// without duplicating the trap.
//
// # It degrades rather than failing
//
// When the input is not a terminal — a pipe, a test, a CI runner — there is no echo to disable
// and nothing to hide, so the answer is read normally. That keeps the existing contract: a
// prompt works from a pipe, and a handler that asks for a secret is still testable.
//
//	token, err := rotini.NewPrompt(rtx.Stdin, rtx.Stdout).
//	    WithLabel("token").WithSecret().Ask(ctx)
func (p *Prompt) WithSecret() *Prompt {
	p.secret = true
	return p
}

// WithMask is [Prompt.WithSecret] that echoes r for each rune typed, the "••••" shape, so a user
// can see their keystrokes register. A zero r is the same as WithSecret.
//
// The mask is written by rotini rather than the terminal, so it appears only where echo was
// actually disabled: on a pipe the answer is read plainly and nothing is masked, because there
// was nothing on screen to mask.
func (p *Prompt) WithMask(r rune) *Prompt {
	p.secret = true
	p.mask = r
	return p
}

// promptLabel renders "label [default]: ".
func promptLabel(label, def string) string {
	if label == "" {
		return ""
	}
	if def != "" {
		return label + " [" + def + "]: "
	}
	return label + ": "
}

// Confirm asks a yes/no question. The accepted answers are configurable, an empty answer takes
// the default, and input that ends without one is [ErrNotInteractive] unless a default was set.
//
// The zero value is not usable; start from [NewConfirm].
type Confirm struct {
	asker

	label       string
	def         *bool
	affirmative []string
	negative    []string
	retries     int
}

// NewConfirm returns a yes/no prompt reading from in and writing to out.
func NewConfirm(in io.Reader, out io.Writer) *Confirm {
	return &Confirm{
		asker:       newAsker(in, out),
		affirmative: []string{"y", "yes"},
		negative:    []string{"n", "no"},
		retries:     defaultRetries,
	}
}

// WithLabel sets the question text.
func (c *Confirm) WithLabel(label string) *Confirm { c.label = label; return c }

// WithDefault sets the answer an empty line (or non-interactive input) resolves
// to, shown as the capitalized choice in "[y/N]". Without one, an empty answer
// re-asks.
func (c *Confirm) WithDefault(value bool) *Confirm { c.def = &value; return c }

// WithAffirmative replaces the accepted "yes" tokens (default y, yes). Matching
// is case-insensitive.
func (c *Confirm) WithAffirmative(tokens ...string) *Confirm {
	if len(tokens) > 0 {
		c.affirmative = tokens
	}
	return c
}

// WithNegative replaces the accepted "no" tokens (default n, no).
func (c *Confirm) WithNegative(tokens ...string) *Confirm {
	if len(tokens) > 0 {
		c.negative = tokens
	}
	return c
}

// WithRetries sets how many times an unrecognized answer may be re-asked
// (default 2). Zero means a single attempt.
func (c *Confirm) WithRetries(n int) *Confirm {
	if n >= 0 {
		c.retries = n
	}
	return c
}

// Ask writes the question and reads until it gets a recognized answer.
func (c *Confirm) Ask(ctx context.Context) (bool, error) {
	for attempt := 0; ; attempt++ {
		c.write(c.label + " " + confirmChoices(c.def) + " ")
		answer, err := c.readLine(ctx)
		if errors.Is(err, ErrNotInteractive) && c.def != nil {
			return *c.def, nil
		}
		if err != nil {
			return false, err
		}
		switch {
		case answer == "" && c.def != nil:
			return *c.def, nil
		case matchToken(answer, c.affirmative):
			return true, nil
		case matchToken(answer, c.negative):
			return false, nil
		}
		if attempt >= c.retries {
			return false, fmt.Errorf("%w: %q is not one of %s", ErrPromptInvalid,
				answer, strings.Join(append(append([]string{}, c.affirmative...), c.negative...), ", "))
		}
	}
}

// confirmChoices renders "[y/n]" with the default capitalized.
func confirmChoices(def *bool) string {
	switch {
	case def == nil:
		return "[y/n]"
	case *def:
		return "[Y/n]"
	default:
		return "[y/N]"
	}
}

func matchToken(answer string, tokens []string) bool {
	answer = strings.TrimSpace(answer)
	for _, t := range tokens {
		if strings.EqualFold(answer, t) {
			return true
		}
	}
	return false
}

// Select asks the user to choose one of a list of options.
//
// The menu is numbered and read line-wise rather than driven by arrow keys: raw terminal mode
// would need a platform dependency rotini does not carry and would not work over a pipe at
// all. A numbered menu stays scriptable and degrades to [ErrNotInteractive] cleanly. An answer
// that is not a number is matched against the option text — exactly, then by fuzzy distance
// with [Select.WithSuggestor].
//
// The zero value is not usable; start from [NewSelect].
type Select struct {
	asker

	label     string
	options   []string
	def       int
	suggestor *Suggestor
	retries   int
}

// NewSelect returns a chooser over options, reading from in and writing the menu
// to out.
func NewSelect(in io.Reader, out io.Writer, options ...string) *Select {
	return &Select{asker: newAsker(in, out), options: options, def: -1, retries: defaultRetries}
}

// WithLabel sets the text written above the menu.
func (s *Select) WithLabel(label string) *Select { s.label = label; return s }

// WithDefault sets the zero-based index an empty answer resolves to, and the answer used when
// input is not interactive. Out-of-range clears it.
func (s *Select) WithDefault(index int) *Select {
	s.def = -1
	if index >= 0 && index < len(s.options) {
		s.def = index
	}
	return s
}

// WithSuggestor lets an answer that is neither a number nor an exact option be resolved by
// fuzzy match. Without one, only numbers and exact text are accepted.
func (s *Select) WithSuggestor(suggestor *Suggestor) *Select { s.suggestor = suggestor; return s }

// WithRetries sets how many times an unrecognized answer may be re-asked
// (default 2). Zero means a single attempt.
func (s *Select) WithRetries(n int) *Select {
	if n >= 0 {
		s.retries = n
	}
	return s
}

// Ask renders the menu and reads a choice, returning its index and text.
func (s *Select) Ask(ctx context.Context) (int, string, error) {
	if len(s.options) == 0 {
		return -1, "", UsageError(errors.New("rotini: select has no options"))
	}
	for attempt := 0; ; attempt++ {
		s.write(s.menu())
		answer, err := s.readLine(ctx)
		if errors.Is(err, ErrNotInteractive) && s.def >= 0 {
			return s.def, s.options[s.def], nil
		}
		if err != nil {
			return -1, "", err
		}
		if answer == "" && s.def >= 0 {
			return s.def, s.options[s.def], nil
		}
		if i := s.resolve(answer); i >= 0 {
			return i, s.options[i], nil
		}
		if attempt >= s.retries {
			return -1, "", fmt.Errorf("%w: %q is not one of the %d options", ErrPromptInvalid, answer, len(s.options))
		}
	}
}

// menu renders the label and the numbered options.
func (s *Select) menu() string {
	var b strings.Builder
	if s.label != "" {
		b.WriteString(s.label)
		b.WriteString("\n")
	}
	for i, opt := range s.options {
		marker := " "
		if i == s.def {
			marker = "*"
		}
		fmt.Fprintf(&b, " %s %d) %s\n", marker, i+1, opt)
	}
	b.WriteString("choice: ")
	return b.String()
}

// resolve maps an answer to an option index: a 1-based number, then an exact
// (case-insensitive) option, then a fuzzy match when a Suggestor is bound.
func (s *Select) resolve(answer string) int {
	answer = strings.TrimSpace(answer)
	if n, err := strconv.Atoi(answer); err == nil {
		if n >= 1 && n <= len(s.options) {
			return n - 1
		}
		return -1 // a number outside the menu is a mistake, not a label to match
	}
	for i, opt := range s.options {
		if strings.EqualFold(opt, answer) {
			return i
		}
	}
	if s.suggestor != nil {
		if best, ok := s.suggestor.Closest(answer, s.options); ok {
			for i, opt := range s.options {
				if opt == best {
					return i
				}
			}
		}
	}
	return -1
}

// readAnswer reads one line, without echo when the prompt is a secret.
//
// The terminal to silence is the one the user TYPES at, which is the input stream — not the
// output the label went to. They are usually the same device and occasionally are not, and
// disabling echo on the wrong one silences nothing.
func (p *Prompt) readAnswer(ctx context.Context) (string, error) {
	if !p.secret {
		return p.readLine(ctx)
	}

	var line string
	var err error
	echoed := withEchoDisabled(p.inputFile(), func() {
		line, err = p.readLine(ctx)
	})

	if !echoed {
		// The user's Enter was not echoed either, so without this the next write lands on
		// the prompt line.
		p.write("\n")
		if p.mask != 0 {
			// The keystrokes were invisible; show the shape of what was typed.
			p.write(strings.Repeat(string(p.mask), len([]rune(line))) + "\n")
		}
	}
	return line, err
}
