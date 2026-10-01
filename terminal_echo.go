package rotini

import "os"

// Terminal echo control, for reading a secret without it appearing on screen.
//
// The ioctl request constants differ between Linux (TCGETS/TCSETS) and the BSDs including
// macOS (TIOCGETA/TIOCSETA), so they live behind build tags, with the shared termios code in
// terminal_echo_termios.go and a no-op for every other platform. Same bargain as
// [TerminalSize]: a little platform tedium to keep rotini's runtime free of a terminal-library
// dependency.

// withEchoDisabled runs fn with terminal echo turned off on file, restoring the previous state
// afterwards — on every path, including a panic. When file is not a terminal, or the platform
// cannot ask, fn runs unchanged: a pipe echoes nothing, so there is nothing to hide.
func withEchoDisabled(file *os.File, fn func()) {
	if restore, ok := disableEcho(file); ok {
		defer restore()
	}
	fn()
}
