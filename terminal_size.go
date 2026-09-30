package rotini

import (
	"os"
	"strconv"
)

// Terminal geometry, opt-in like everything else in detect.go: rotini never measures the
// terminal for you, and nothing in the runtime calls [TerminalSize]. It is here so the library
// that DOES wrap or draw your output has a width to work with, without you taking a dependency
// to learn one number.
//
// The platform half lives in terminal_size_unix.go and terminal_size_other.go behind build tags,
// because asking the kernel how wide a terminal is needs an ioctl the standard library does not
// export. That is a few dozen lines of platform tedium in exchange for keeping rotini's runtime
// free of a terminal-library dependency: a CLI should not pull in a terminal library to learn one number.

// TerminalSize reports the size of the terminal behind stream, in character cells. Like
// [IsTerminal], it takes a handler's streams as they are — rtx.Stdout, not a type assertion.
//
// It answers ok=false when stream is not a terminal, when the platform has no way to ask, or when
// the answer would be nonsense — so a caller can always write:
//
//	cols, _, ok := rotini.TerminalSize(rtx.Stdout)
//	if !ok {
//	    cols = 80
//	}
//	fmt.Fprintln(rtx.Stdout, wrap(text, cols))
//
// **COLUMNS and LINES win when set.** Those are the conventional override — `COLUMNS=40 mycli`
// is how a user asks for a narrower render, and how a test pins one — so they are consulted
// before the kernel. A value that is not a positive integer is ignored rather than honored as
// zero.
//
// Measure the stream you are about to write to. A program piping stdout to a file while a human
// watches stderr has two different answers, and only the caller knows which one matters.
func TerminalSize(stream any) (cols, rows int, ok bool) {
	envCols, hasCols := positiveEnv("COLUMNS")
	envRows, hasRows := positiveEnv("LINES")
	if hasCols && hasRows {
		return envCols, envRows, true
	}

	file, _ := stream.(*os.File) // only a file can be a terminal; a nil *os.File is handled below
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
