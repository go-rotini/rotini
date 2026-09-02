package rotini

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func newAsker(in io.Reader, out io.Writer) asker {
	return asker{in: bufio.NewReader(in), out: out}
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
		line, err := a.in.ReadString('\n')
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
		answer, err := p.readLine(ctx)
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
