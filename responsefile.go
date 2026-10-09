package rotini

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// responseFileCap is the largest response file read, in bytes.
const responseFileCap = 1 << 20

// expandResponseFiles replaces each response-file word of argv with the file's words. Before
// the first "--" (typed, or read from a file), a word longer than prefix that starts with it
// names a file, read relative to the run's directory: one word per line, a trailing "\r"
// removed, and empty, blank and "#" comment lines skipped. Words read from a file are inserted
// as they are, never expanded again. A word starting with the prefix twice stands for itself
// with one prefix removed ("@@x" is the word "@x"), and a bare prefix is an ordinary word.
//
// A file that can't be read is a usage [*ParseError] naming the word; with skipUnreadable it is
// dropped instead, as completion does with a half-typed line.
func expandResponseFiles(argv []string, prefix string, view *osView, skipUnreadable bool) ([]string, error) {
	if prefix == "" || !hasResponseWord(argv, prefix) {
		return argv, nil
	}
	out := make([]string, 0, len(argv))
	for i, w := range argv {
		if w == "--" {
			return append(out, argv[i:]...), nil
		}
		name, ok := strings.CutPrefix(w, prefix)
		switch {
		case !ok || name == "":
			out = append(out, w)
		case strings.HasPrefix(name, prefix):
			out = append(out, name)
		default:
			words, err := readResponseFile(view, name)
			if err != nil {
				if skipUnreadable {
					continue
				}
				return nil, &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("could not read response file %q: %v", name, err), Token: w}
			}
			for j, rw := range words {
				if rw == "--" {
					out = append(out, words[j:]...)
					return append(out, argv[i+1:]...), nil
				}
				out = append(out, rw)
			}
		}
	}
	return out, nil
}

// hasResponseWord reports whether any word before the first "--" starts with prefix, so argv
// without one is returned as is.
func hasResponseWord(argv []string, prefix string) bool {
	for _, w := range argv {
		if w == "--" {
			return false
		}
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

// readResponseFile reads the words of the response file at path, resolved against the run's
// directory. The error says why in rotini's words, not the platform's.
func readResponseFile(view *osView, path string) ([]string, error) {
	f, err := os.Open(view.abs(path))
	if err != nil {
		return nil, responseFileProblem(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, responseFileCap+1))
	if err != nil {
		return nil, responseFileProblem(err)
	}
	if len(data) > responseFileCap {
		return nil, errors.New("larger than 1 MiB")
	}
	return responseFileWords(string(data)), nil
}

// responseFileProblem names why a response file could not be read.
func responseFileProblem(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errors.New("no such file")
	case errors.Is(err, fs.ErrPermission):
		return errors.New("permission denied")
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Op == "read" {
		return errors.New("not a file")
	}
	return errors.New("cannot read it")
}

// responseFileWords splits a response file's text into words: one per line, a leading
// byte-order mark and a trailing "\r" removed, skipping empty and blank lines and lines whose
// first non-blank character is '#'. Other lines are kept exactly, spaces included.
func responseFileWords(text string) []string { return acquiredLines(text, false, true) }

// completionWords expands the response files among the words before the cursor, skipping any
// that can't be read, so completion walks the words the run would. It reports whether the
// word under the cursor names a response file itself, which leaves it to the shell's file
// completion.
func completionWords(words []string, prefix string, view *osView) ([]string, bool) {
	if prefix == "" || len(words) == 0 {
		return words, false
	}
	context, cursor := words[:len(words)-1], words[len(words)-1]
	expanded, err := expandResponseFiles(context, prefix, view, true)
	if err != nil {
		expanded = context // unreachable: unreadable files are skipped
	}
	if !slices.Contains(expanded, "--") && strings.HasPrefix(cursor, prefix) && !strings.HasPrefix(cursor[len(prefix):], prefix) {
		return nil, true
	}
	return append(append([]string(nil), expanded...), cursor), false
}
