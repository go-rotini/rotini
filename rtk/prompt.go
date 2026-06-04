package rtk

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	xterm "golang.org/x/term"
)

// ErrCanceled is returned by an interactive prompt the user deliberately aborted (Esc,
// q, or Ctrl-C in [Prompter.SelectArrow]). Classify it with errors.Is to tell a
// deliberate cancel apart from a read error or a real answer:
//
//	i, err := pr.SelectArrow("Pick one", opts)
//	switch {
//	case errors.Is(err, rtk.ErrCanceled): // user backed out
//	case err != nil:                       // read failure
//	}
var ErrCanceled = errors.New("rotini: prompt canceled")

// Prompter is rtk's interactive-prompting service: it asks the user questions on the
// output stream and reads answers from the input stream. The everyday prompts are
// line-based, so they work the same whether input is a terminal or a pipe (scripted);
// only [Prompter.Secret] needs a real terminal, where it reads without echo.
//
// Like the other rtk services it is bound once and retrieved by handlers — and it is
// what the `rotini init` wizard uses:
//
//	// main.go
//	rth.Program.Bind("prompt", rtk.NewPrompter()).Execute()
//
//	// a handler
//	pr := rotini.MustGet[*rtk.Prompter](rtx, "prompt")
//	name, _ := pr.LineDefault("Project name", "mycli")
//	i, _ := pr.Select("Config format", []string{"yaml", "jsonc", "json"})
//
// A read error (including EOF / Ctrl-D where no usable answer was given) is returned;
// for prompts with a default, EOF resolves to the default.
type Prompter struct {
	in    io.Reader
	out   io.Writer
	stdin *os.File // set when in is a file, for no-echo Secret reads
	buf   *bufio.Reader
}

// NewPrompter returns a Prompter reading os.Stdin and writing os.Stdout, ready to bind
// under the "prompt" registry key.
func NewPrompter() *Prompter {
	return &Prompter{in: os.Stdin, out: os.Stdout, stdin: os.Stdin}
}

// WithInput replaces the input source (resetting the read buffer). A non-file reader
// (e.g. a test's strings.Reader) disables no-echo for [Prompter.Secret].
func (p *Prompter) WithInput(r io.Reader) *Prompter {
	p.in, p.buf = r, nil
	if f, ok := r.(*os.File); ok {
		p.stdin = f
	} else {
		p.stdin = nil
	}
	return p
}

// WithOutput replaces the stream the questions are written to.
func (p *Prompter) WithOutput(w io.Writer) *Prompter { p.out = w; return p }

// Line asks question and returns the entered line (trimmed of its newline). An empty
// line is returned as "". It errors on EOF with no input.
func (p *Prompter) Line(question string) (string, error) {
	fmt.Fprintf(p.out, "%s: ", question)
	return p.readLine()
}

// LineDefault asks question showing def in brackets; an empty answer (or EOF) yields
// def.
func (p *Prompter) LineDefault(question, def string) (string, error) {
	fmt.Fprintf(p.out, "%s [%s]: ", question, def)
	line, err := p.readLine()
	if err != nil {
		if err == io.EOF {
			return def, nil
		}
		return def, err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// LineValid asks question and re-prompts until validate accepts the entered line,
// printing validate's error as the reason before each re-ask. It returns the first
// accepted line, or a read error (EOF) — a validator is not consulted on a read failure.
// A nil validate behaves like [Prompter.Line].
func (p *Prompter) LineValid(question string, validate func(string) error) (string, error) {
	for {
		line, err := p.Line(question)
		if err != nil {
			return line, err
		}
		if validate == nil {
			return line, nil
		}
		if vErr := validate(line); vErr != nil {
			fmt.Fprintf(p.out, "%v\n", vErr)
			continue
		}
		return line, nil
	}
}

// Confirm asks a yes/no question, re-prompting until the answer is recognizable. An
// empty answer yields def; EOF returns def with the read error.
func (p *Prompter) Confirm(question string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(p.out, "%s [%s]: ", question, hint)
		line, err := p.readLine()
		if err != nil {
			return def, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, `Please answer "y" or "n".`)
	}
}

// Select presents a numbered list of options and returns the chosen index. The user
// enters the number (1-based) or types an option verbatim (case-insensitive);
// anything else re-prompts. It errors when options is empty or on a read error.
func (p *Prompter) Select(question string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("rotini: Select requires at least one option")
	}
	for {
		fmt.Fprintf(p.out, "%s\n", question)
		for i, o := range options {
			fmt.Fprintf(p.out, "  %d) %s\n", i+1, o)
		}
		fmt.Fprintf(p.out, "Enter a number [1-%d]: ", len(options))

		line, err := p.readLine()
		if err != nil {
			return 0, err
		}
		choice := strings.TrimSpace(line)
		if n, e := strconv.Atoi(choice); e == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		for i, o := range options {
			if strings.EqualFold(choice, o) {
				return i, nil
			}
		}
		fmt.Fprintf(p.out, "Please enter a number between 1 and %d.\n", len(options))
	}
}

// SelectArrow is the arrow-key counterpart to [Prompter.Select]: on a real terminal it
// draws the options as an in-place list and lets the user move the highlight with the
// Up/Down arrows (or k/j), confirming with Enter to return the highlighted index. Esc, q,
// or Ctrl-C aborts with [ErrCanceled]. When input is NOT a terminal — a pipe, a test
// reader, or a terminal that cannot enter raw mode — it transparently falls back to the
// line-based [Prompter.Select], so scripted runs keep working unchanged. It errors when
// options is empty.
func (p *Prompter) SelectArrow(question string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("rotini: SelectArrow requires at least one option")
	}
	// Char-at-a-time, no-echo navigation needs a real terminal; otherwise degrade to the
	// line-based prompt (same gate as Secret).
	if p.stdin == nil || !isTTY(p.stdin) {
		return p.Select(question, options)
	}
	st, err := xterm.MakeRaw(int(p.stdin.Fd()))
	if err != nil {
		return p.Select(question, options)
	}
	defer func() { _ = xterm.Restore(int(p.stdin.Fd()), st) }()
	return p.selectLoop(p.stdin, question, options)
}

// selectLoop runs the arrow-key picker over in (the raw terminal, delivering one
// keystroke per Read) and p.out. It is split from [Prompter.SelectArrow]'s terminal
// setup so it can be driven over plain readers/writers in tests. The terminal is assumed
// already raw, so lines are CRLF-terminated (raw mode does no \n→\r\n translation) and
// the cursor is hidden for the duration.
func (p *Prompter) selectLoop(in io.Reader, question string, options []string) (int, error) {
	fmt.Fprint(p.out, HideCursor)
	defer fmt.Fprint(p.out, ShowCursor)
	fmt.Fprintf(p.out, "%s\r\n", question)

	cursor := 0
	fmt.Fprint(p.out, renderSelectOptions(options, cursor))

	buf := make([]byte, 8)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			switch decodeSelectKey(buf[:n]) {
			case keyUp:
				cursor = (cursor - 1 + len(options)) % len(options)
				p.redrawSelect(options, cursor)
			case keyDown:
				cursor = (cursor + 1) % len(options)
				p.redrawSelect(options, cursor)
			case keyEnter:
				return cursor, nil
			case keyCancel:
				return 0, ErrCanceled
			case keyNone:
				// ignored key — no redraw
			}
		}
		if err != nil {
			if err == io.EOF {
				return 0, ErrCanceled // input ended before a choice was made
			}
			return 0, err
		}
	}
}

// redrawSelect moves the cursor back to the top of the options block and rewrites it in
// place over the previous frame.
func (p *Prompter) redrawSelect(options []string, cursor int) {
	fmt.Fprint(p.out, CursorUp(len(options))+renderSelectOptions(options, cursor))
}

// renderSelectOptions renders the options block: one CRLF-terminated line per option,
// the current row marked "> " (others "  "), every line cleared first so a redraw leaves
// no residue from a longer previous label.
func renderSelectOptions(options []string, cursor int) string {
	var b strings.Builder
	for i, o := range options {
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		b.WriteString("\r" + ClearLine + marker + o + "\r\n")
	}
	return b.String()
}

// selectKey is a decoded navigation action from a raw input chunk.
type selectKey int

const (
	keyNone selectKey = iota
	keyUp
	keyDown
	keyEnter
	keyCancel
)

// decodeSelectKey maps one raw keystroke to a navigation action: the Up/Down arrow
// escape sequences (ESC [ A / ESC [ B) and their vim equivalents (k/j), Enter (CR or
// LF), and cancel (Ctrl-C, q, or a lone Esc). Anything else is keyNone. It assumes b is
// a single keystroke (a raw terminal delivers one per Read).
func decodeSelectKey(b []byte) selectKey {
	if len(b) >= 3 && b[0] == 0x1b && b[1] == '[' {
		switch b[2] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		}
		return keyNone
	}
	if len(b) == 1 {
		switch b[0] {
		case '\r', '\n':
			return keyEnter
		case 'k':
			return keyUp
		case 'j':
			return keyDown
		case 0x03, 'q', 0x1b: // Ctrl-C, q, lone Esc
			return keyCancel
		}
	}
	return keyNone
}

// Secret asks question and reads the answer without echoing it, when the input is a
// terminal (so a password is not shown). On a pipe or a non-file input it falls back
// to a normal line read.
func (p *Prompter) Secret(question string) (string, error) {
	fmt.Fprintf(p.out, "%s: ", question)
	if p.stdin != nil && isTTY(p.stdin) {
		b, err := xterm.ReadPassword(int(p.stdin.Fd()))
		fmt.Fprintln(p.out) // ReadPassword swallows the newline; echo one
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return p.readLine()
}

// SecretConfirm reads a secret twice (the value, then a confirmation) without echoing,
// re-prompting both until the two entries match — the standard "enter a new password,
// confirm it" flow. It returns the confirmed value, or a read error (EOF). The
// confirmation prompt is "Confirm <question>".
func (p *Prompter) SecretConfirm(question string) (string, error) {
	for {
		first, err := p.Secret(question)
		if err != nil {
			return "", err
		}
		again, err := p.Secret("Confirm " + question)
		if err != nil {
			return "", err
		}
		if first == again {
			return first, nil
		}
		fmt.Fprintln(p.out, "Entries do not match — please try again.")
	}
}

// readLine reads one line from the input, trimming the trailing newline. A final line
// without a newline (EOF-terminated) is still returned; EOF with nothing read is an
// error.
func (p *Prompter) readLine() (string, error) {
	if p.buf == nil {
		p.buf = bufio.NewReader(p.in)
	}
	s, err := p.buf.ReadString('\n')
	s = strings.TrimRight(s, "\r\n")
	if err != nil {
		if err == io.EOF && s != "" {
			return s, nil
		}
		return s, err
	}
	return s, nil
}
