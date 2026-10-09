package rotini

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The input-file and output-file kinds: a path, or "-" for the run's stdin or stdout. Their
// values are plain strings; [OpenInput] and [CreateOutput] open them.
const (
	typeInputFile  = "inputfile"
	typeOutputFile = "outputfile"
)

// isStreamPathType reports whether typ is inputfile or outputfile, the path kinds where "-"
// names a standard stream.
func isStreamPathType(typ string) bool { return typ == typeInputFile || typ == typeOutputFile }

// checkStreamPath checks an inputfile or outputfile value at parse time. "-" always passes.
// An input file must exist and not be a directory, as an existingfile must. An output file
// need not exist, but must not be a directory, and its directory must exist; whether it can be
// written is left to [CreateOutput].
func checkStreamPath(label, typ, value, dir string) error {
	if value == "-" {
		return nil
	}
	if typ == typeInputFile {
		return checkPathExists(label, "existingfile", value, dir)
	}
	full := joinDir(dir, value)
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %q is a directory", label, value), Flag: label}
	}
	if info, err := os.Stat(filepath.Dir(full)); errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		parent := filepath.Dir(value) + string(filepath.Separator)
		return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: no such directory: %q", label, parent), Flag: label}
	}
	return nil
}

// dashOnce tracks the inputfile values that name stdin ("-") across one run's inputs: stdin can
// be read only once, so only one of them may name it.
type dashOnce struct {
	first string // the label of the input that already reads stdin
}

// seen checks vals, the values of the input label (flag is its flag label, "" for an argument).
func (d *dashOnce) seen(label, flag string, vals []string) error {
	for _, v := range vals {
		if v != "-" {
			continue
		}
		if d.first != "" {
			return &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: %q (stdin) can be given only once; %s already reads it", label, "-", d.first),
				Flag: flag,
			}
		}
		d.first = label
	}
	return nil
}

// stdinDashOnce rejects a second "-" among the command line's inputfile values.
func stdinDashOnce(chain []Command, store *parsedInputs) error {
	var dash dashOnce
	seen := dash.seen
	for i, c := range chain {
		if i >= len(store.scopes) {
			break
		}
		si := store.scopes[i]
		for _, fd := range c.Flags {
			if constraintElemType(fd.Type) != typeInputFile || !store.setOnArgv(i, fd.Name) {
				continue
			}
			if err := seen(si.label(fd), si.label(fd), si.flags[fd.Name]); err != nil {
				return err
			}
		}
	}
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]
	args := si.args[:len(si.args)-si.placeholderArgs]
	for i, s := range argSpans(leaf.Arguments, len(args)) {
		ad := leaf.Arguments[i]
		if constraintElemType(ad.Type) != typeInputFile || s[0] >= s[1] || si.handBuiltArgs[i] {
			continue
		}
		if err := seen("<"+ad.Name+">", "", args[s[0]:s[1]]); err != nil {
			return err
		}
	}
	return nil
}
