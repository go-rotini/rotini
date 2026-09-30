package rotini

import (
	"errors"
	"io"
	"os"
)

// What rotini keeps of the terminal-as-a-device: the two questions a program must answer before
// choosing a UI library, and the one operation the standard library will not do for it.
//
// rotini does NOT ship a prompt, a spinner, a table or a styler. Those are solved, and solved
// better, by libraries built for them — charmbracelet/huh for forms, lipgloss for styling and
// tables, bubbles for indicators, muesli/reflow for wrapping. A CLI framework's job is the part
// nobody else can do for you: turning a spec into a parsed, bound, dispatched invocation. What
// remains here is the platform tedium underneath those libraries' decisions, which is small,
// stable, and needs no dependency.

// ErrNotInteractive reports that input reached EOF without an answer. It is what makes an
// interactive step safe in a pipeline or CI job: the run fails fast and says why instead of
// blocking on a stdin nobody is typing into. It is an [ErrUsage] — the environment, not the
// program, is wrong.
//
// A [REPL] line reader returns it to end the session cleanly, and a program driving its own
// prompts should adopt the same contract.
var ErrNotInteractive = UsageError(errors.New("no input available (not interactive)"))

// ReadSecret reads one line from r without echoing it, for a password, token or passphrase.
//
// This is the one piece of interactive input rotini keeps, because it is the one the standard
// library cannot do and a program cannot safely fake: it needs a termios ioctl to clear the ECHO
// bit, and it must put the bit back on every path. The failure mode is not a wrong value — it is
// A SHELL LEFT WITH ECHO OFF, which survives the process and confuses the user's next command.
//
// Echo is restored before returning, including on error. When r is not a terminal — a pipe, a
// test's buffer, a CI runner — there is no echo to disable and the line is read normally, which
// keeps a secret-reading command testable and scriptable with the same code: pass rtx.Stdin.
//
// Echo control needs a termios ioctl, which rotini wires on Linux, macOS and the BSDs. On any
// other platform (Windows among them) the line is read normally and the terminal echoes it.
//
// The trailing newline is consumed and not returned. Nothing is written to the screen, so a
// caller that printed a prompt should print its own newline afterwards: the user's Enter was not
// echoed either.
//
//	fmt.Fprint(rtx.Stdout, "token: ")
//	secret, err := rotini.ReadSecret(rtx.Stdin)
//	fmt.Fprintln(rtx.Stdout)
func ReadSecret(r io.Reader) ([]byte, error) {
	if r == nil || isNilPointer(r) {
		return nil, ErrNotInteractive
	}
	file, ok := r.(*os.File)
	if !ok {
		return readSecretLine(r)
	}
	var out []byte
	var err error
	withEchoDisabled(file, func() {
		out, err = readSecretLine(file)
	})
	return out, err
}

// readSecretLine reads bytes up to the first newline. It reads one byte at a time rather than
// buffering: a buffered reader would consume past the newline, swallowing input that belongs to
// whatever the program does next.
func readSecretLine(r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			switch buf[0] {
			case '\n':
				return trimCR(out), nil
			default:
				out = append(out, buf[0])
			}
		}
		if err != nil {
			if len(out) == 0 {
				return nil, ErrNotInteractive
			}
			return trimCR(out), nil
		}
	}
}

// trimCR drops a trailing carriage return, so a CRLF terminal does not leak one into the secret.
func trimCR(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		return b[:len(b)-1]
	}
	return b
}
