package rotini

import "reflect"

// IsTerminal reports whether stream is attached to a terminal: an *os.File, or any value with
// an Fd() uintptr method, such as [Context.Stdout] under [Program.WithBufferedOutput].
// Anything else, a nil value included, is not a terminal. A program uses it to pick a default
// output format or to skip a prompt when its output is piped:
//
//	format := in.Flags.Format
//	if format == "" && !rotini.IsTerminal(rtx.Stdout) {
//		format = "json"
//	}
//
// It checks the device itself, so /dev/null, a character device that is not a terminal,
// reports false. On Windows only a console counts: a mintty or Cygwin pseudo-terminal is a
// pipe to the program, so it reports false. This is the one terminal check rotini offers; it
// still never styles output or prompts.
func IsTerminal(stream any) bool {
	f, ok := stream.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	if v := reflect.ValueOf(stream); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	fd := f.Fd()
	if fd == ^uintptr(0) {
		return false
	}
	return isTerminalFd(fd)
}
