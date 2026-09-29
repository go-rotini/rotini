package rotini

import "os"

// Terminal echo control, for reading a secret without it appearing on screen.
//
// The ioctl constants differ between Linux (TCGETS/TCSETS) and the BSDs including macOS
// (TIOCGETA/TIOCSETA), so the two live behind build tags alongside a no-op for every other
// platform. Same bargain as [TerminalSize]: a little platform tedium to keep rotini's runtime
// free of external dependencies.

// withEchoDisabled runs fn with terminal echo turned off on file, restoring the previous state
// afterwards — on every path, including a panic.
//
// When file is not a terminal, or the platform cannot ask, fn runs unchanged and echoed reports
// true. A caller uses that to decide whether it must mask the input itself: a pipe echoes
// nothing, so there is nothing to hide.
func withEchoDisabled(file *os.File, fn func()) (echoed bool) {
	restore, ok := disableEcho(file)
	if !ok {
		fn()
		return true
	}
	defer restore()
	fn()
	return false
}
