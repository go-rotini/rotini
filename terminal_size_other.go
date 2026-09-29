//go:build !unix

package rotini

import "os"

// terminalSize has no portable implementation off unix. Reporting "no answer" is the honest
// result: [TerminalSize] still honors COLUMNS and LINES, so a program remains configurable, and
// a caller's fallback is the same code it already needed for a pipe.
func terminalSize(file *os.File) (cols, rows int, ok bool) { return 0, 0, false }
