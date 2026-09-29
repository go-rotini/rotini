package rotini

import (
	"os"
	"strconv"
)

// Terminal geometry, opt-in like everything else in detect.go: rotini never measures the
// terminal for you, and nothing in the runtime calls [TerminalSize].
//
// The platform half lives in terminal_size_unix.go and terminal_size_other.go behind build tags,
// because asking the kernel how wide a terminal is needs an ioctl the standard library does not
// export. That is sixty lines of platform tedium in exchange for keeping rotini's runtime free of
// external dependencies — see the batteries audit's dependency decision.

// TerminalSize reports the size of the terminal behind file, in character cells.
//
// It answers ok=false when file is not a terminal, when the platform has no way to ask, or when
// the answer would be nonsense — so a caller can always write:
//
//	cols, _, ok := rotini.TerminalSize(os.Stdout)
//	if !ok {
//	    cols = 80
//	}
//	fmt.Fprintln(rtx.Stdout, rotini.Wrap(text, cols))
//
// **COLUMNS and LINES win when set.** Those are the conventional override — `COLUMNS=40 mycli`
// is how a user asks for a narrower render, and how a test pins one — so they are consulted
// before the kernel. A value that is not a positive integer is ignored rather than honored as
// zero.
//
// Measure the stream you are about to write to. A program piping stdout to a file while a human
// watches stderr has two different answers, and only the caller knows which one matters.
func TerminalSize(file *os.File) (cols, rows int, ok bool) {
	envCols, hasCols := positiveEnv("COLUMNS")
	envRows, hasRows := positiveEnv("LINES")
	if hasCols && hasRows {
		return envCols, envRows, true
	}

	c, r, got := terminalSize(file)
	switch {
	case hasCols && got:
		return envCols, r, true
	case hasRows && got:
		return c, envRows, true
	case got:
		return c, r, true
	case hasCols:
		// An override without a terminal is still an answer for the axis it names.
		return envCols, 0, true
	case hasRows:
		return 0, envRows, true
	}
	return 0, 0, false
}

// positiveEnv reads name as a positive integer, reporting false for unset, unparseable or
// non-positive — a "0" override is meaningless and must not be mistaken for an answer.
func positiveEnv(name string) (int, bool) {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}
