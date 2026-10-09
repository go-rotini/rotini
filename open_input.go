package rotini

import (
	"errors"
	"io"
	"os"
)

// OpenInput opens an input-file value (a spec's `type: inputfile`) for reading:
//
//   - "-" is the run's stdin, read byte for byte with no line-ending or byte-order-mark
//     changes. Every "-" in a run reads the same single stream, and closing it leaves stdin
//     open. When stdin is a terminal, OpenInput returns a usage error rather than wait for
//     typing. A read that the run's signal interrupts returns an [*InputError] and stops the
//     run with the signal's exit code.
//   - Any other path is opened relative to the run's directory ([Context.Dir]); a file that
//     cannot be opened is a usage error naming the path as given.
//
// A handler for `cat [FILE...]` reads each value in turn, treating no files as "-":
//
//	files := in.Arguments.Files
//	if len(files) == 0 {
//		files = []string{"-"}
//	}
//	for _, name := range files {
//		f, err := rotini.OpenInput(rtx, name)
//		if err != nil {
//			return err
//		}
//		_, err = io.Copy(rtx.Stdout, f)
//		f.Close()
//		if err != nil {
//			return err
//		}
//	}
//
// Use "-" rather than /dev/stdin: a path is opened on its own and would not share the stream
// another stdin consumer reads.
func OpenInput(rtx *Context, path string) (io.ReadCloser, error) {
	if path == "-" {
		if IsTerminal(rtx.Stdin) {
			return nil, UsageError(errors.New(`"-" reads piped stdin, but stdin is a terminal`))
		}
		r := rtx.stdinStream() // a stdin that isn't piped (/dev/null) reads as empty
		return io.NopCloser(r), nil
	}
	full := rtx.osView().abs(path)
	f, err := os.Open(full)
	if err != nil {
		return nil, UsageError(&pathError{msg: unreadableFile(path, full, err), err: err})
	}
	if info, err := f.Stat(); err == nil && info.IsDir() {
		_ = f.Close()
		return nil, UsageError(&pathError{msg: unreadableFile(path, full, errIsDir)})
	}
	return f, nil
}

// errIsDir is any error that is not a missing file or a permission problem, so
// unreadableFile names the directory.
var errIsDir = errors.New("is a directory")
