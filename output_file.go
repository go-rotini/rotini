package rotini

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// OutputFile is a file being written by [CreateOutput]. It is an [io.WriteCloser]: Close
// commits what was written and Abort discards it. Both are idempotent, and Close after Abort
// does nothing. The idiom commits only on success:
//
//	out, err := rotini.CreateOutput(rtx, in.Flags.Output, rotini.Overwrite(in.Flags.Force))
//	if err != nil {
//		return err
//	}
//	defer out.Abort()
//	if err := rtx.WriteOutputTo(out, report, "json", nil); err != nil {
//		return err
//	}
//	return out.Close()
type OutputFile struct {
	mu        sync.Mutex
	rtx       *Context
	name      string    // the path as given
	w         io.Writer // where writes go
	file      *os.File  // nil when writing to stdout
	tmp       string    // the temporary file Close renames onto target; "" for a direct write
	target    string
	overwrite bool
	werr      error // the first write error
	done      bool
}

// OutputOption configures [CreateOutput].
type OutputOption func(*outputConfig)

type outputConfig struct {
	overwrite bool
	mode      fs.FileMode
	modeSet   bool
}

// Overwrite lets [CreateOutput] replace an existing file. Without it, or with
// Overwrite(false), an existing file is a usage error. A command usually passes the value of
// a --force flag it declares:
//
//	rotini.CreateOutput(rtx, in.Flags.Output, rotini.Overwrite(in.Flags.Force))
func Overwrite(enabled bool) OutputOption {
	return func(c *outputConfig) { c.overwrite = enabled }
}

// FileMode sets the permissions of a file [CreateOutput] creates or replaces, before the
// umask. The default is 0644 for a new file, and the replaced file's own permissions when
// overwriting; FileMode(0o600) keeps secret output private.
func FileMode(perm fs.FileMode) OutputOption {
	return func(c *outputConfig) { c.mode, c.modeSet = perm.Perm(), true }
}

// CreateOutput opens an output-file value for writing (a spec's `type: outputfile`):
//
//   - "-" is the run's stdout, [Context.Stdout]; Close and Abort do nothing.
//   - A path is written atomically: to a temporary file beside it, renamed into place by
//     Close. Abort, a failed write or a failed Close removes the temporary file and leaves an
//     existing file untouched, and so does a run that ends with the file still open, by a
//     return, a halt or a panic. A relative path is resolved against the run's directory
//     ([Context.Dir]).
//   - An existing file that is not a regular file, such as /dev/null, a named pipe or NUL on
//     Windows, is written directly, not atomically.
//
// An existing regular file is replaced only with [Overwrite](true); otherwise CreateOutput
// returns a usage error that matches [fs.ErrExist]. The check is repeated before the rename,
// but a file created in between is still replaced. A symbolic link is followed, so the file
// it points to is replaced and the link is kept. Replacing a file gives it a new identity: hard
// links to the old one, its owner and its extended attributes are not carried over. On Windows,
// the rename is retried briefly while another program holds the file open, and a read-only
// file cannot be replaced.
//
// The file is not synced to disk: Close makes it visible whole, as most tools that write
// files do, not durable across a power loss.
func CreateOutput(rtx *Context, path string, opts ...OutputOption) (*OutputFile, error) {
	var cfg outputConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if path == "" {
		return nil, UsageError(errors.New("no output file was given"))
	}
	if path == "-" {
		return &OutputFile{rtx: rtx, name: path, w: rtx.Stdout}, nil
	}

	target := rtx.osView().abs(path)
	info, err := os.Lstat(target)
	exists := err == nil
	if exists && info.Mode()&fs.ModeSymlink != 0 {
		if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
			target = resolved
			info, err = os.Stat(target)
			exists = err == nil
		}
	}
	switch {
	case exists && info.IsDir():
		return nil, UsageError(&pathError{msg: fmt.Sprintf("output file %q is a directory", path)})
	case exists && !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0:
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return nil, &pathError{msg: fmt.Sprintf("could not open output file %q", path), err: err, detail: true}
		}
		return rtx.trackOutput(&OutputFile{rtx: rtx, name: path, w: f, file: f, target: target}), nil
	case exists && !cfg.overwrite:
		return nil, existsError(path)
	}

	perm := fs.FileMode(0o644)
	keepMode := false
	switch {
	case cfg.modeSet:
		perm = cfg.mode
	case exists:
		perm, keepMode = info.Mode().Perm(), true
	}
	f, tmp, err := createTemp(target, perm)
	if err != nil {
		return nil, &pathError{msg: fmt.Sprintf("could not create output file %q", path), err: err, detail: true}
	}
	if keepMode {
		// The temporary file was created through the umask; give it the replaced file's mode.
		if err := f.Chmod(perm); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return nil, &pathError{msg: fmt.Sprintf("could not create output file %q", path), err: err, detail: true}
		}
	}
	return rtx.trackOutput(&OutputFile{
		rtx: rtx, name: path, w: f, file: f, tmp: tmp, target: target, overwrite: cfg.overwrite,
	}), nil
}

// createTemp creates a new file beside target, named after it, with perm before the umask.
func createTemp(target string, perm fs.FileMode) (*os.File, string, error) {
	dir, base := filepath.Split(target)
	for range 100 {
		name := filepath.Join(dir, "."+base+"."+strconv.FormatUint(rand.Uint64(), 36)+".tmp")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, name, err //nolint:wrapcheck // wrapped by the caller
	}
	return nil, "", errors.New("could not find a free temporary name")
}

// Write writes p to the file, or to stdout for "-".
func (f *OutputFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return 0, fs.ErrClosed
	}
	n, err := f.w.Write(p)
	if err != nil && f.werr == nil {
		f.werr = err
	}
	return n, err
}

// Name is the path as given to [CreateOutput], "-" for stdout.
func (f *OutputFile) Name() string { return f.name }

// Close commits the file: the temporary file is closed and renamed into place. If a write
// failed, or closing or renaming fails (a full disk often shows only here), the temporary file
// is removed, an existing file is left as it was, and the error is returned. Close on stdout,
// or after Close or Abort, does nothing.
func (f *OutputFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return nil
	}
	f.done = true
	f.rtx.untrackOutput(f)
	if f.file == nil {
		return nil
	}
	cerr := f.file.Close()
	if f.tmp == "" {
		if err := cmp.Or(f.werr, cerr); err != nil {
			return &pathError{msg: fmt.Sprintf("could not write output file %q", f.name), err: err, detail: true}
		}
		return nil
	}
	if err := cmp.Or(f.werr, cerr); err != nil {
		_ = os.Remove(f.tmp)
		return &pathError{msg: fmt.Sprintf("could not write output file %q", f.name), err: err, detail: true}
	}
	if !f.overwrite {
		if _, err := os.Lstat(f.target); err == nil {
			_ = os.Remove(f.tmp)
			return existsError(f.name)
		}
	}
	if err := renameOutput(f.tmp, f.target); err != nil {
		_ = os.Remove(f.tmp)
		return &pathError{msg: fmt.Sprintf("could not replace output file %q", f.name), err: err, detail: true}
	}
	return nil
}

// Abort discards the file: the temporary file is closed and removed, and an existing file is
// left as it was. A file written directly (a device or a named pipe) is only closed. Abort on
// stdout, or after Close or Abort, does nothing.
func (f *OutputFile) Abort() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return nil
	}
	f.done = true
	f.rtx.untrackOutput(f)
	if f.file == nil {
		return nil
	}
	_ = f.file.Close() // the file is discarded, so a close error changes nothing
	if f.tmp == "" {
		return nil
	}
	if err := os.Remove(f.tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &pathError{msg: fmt.Sprintf("could not remove the temporary file for %q", f.name), err: err, detail: true}
	}
	return nil
}

// existsError is the error for an output file that exists and may not be replaced.
func existsError(path string) error {
	return UsageError(&pathError{msg: fmt.Sprintf("output file %q already exists", path), err: fs.ErrExist})
}

// pathError is a file error in rotini's words that still matches its cause with errors.Is.
// With detail, the message ends with the operating system's reason ("no space left on
// device"), without the path it names.
type pathError struct {
	msg    string
	err    error
	detail bool
}

func (e *pathError) Error() string {
	if !e.detail || e.err == nil {
		return e.msg
	}
	cause := e.err
	if pe, ok := errors.AsType[*fs.PathError](cause); ok {
		cause = pe.Err
	}
	if le, ok := errors.AsType[*os.LinkError](cause); ok {
		cause = le.Err
	}
	return e.msg + ": " + cause.Error()
}

func (e *pathError) Unwrap() error { return e.err }

// trackOutput registers f with the run, which aborts it when the run settles with f still
// open. It returns f.
func (rtx *Context) trackOutput(f *OutputFile) *OutputFile {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.outputs = append(rtx.outputs, f)
	return f
}

func (rtx *Context) untrackOutput(f *OutputFile) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.outputs = slices.DeleteFunc(rtx.outputs, func(o *OutputFile) bool { return o == f })
}

// abortOutputs discards every output file the run left open; a temporary file that can't be
// removed is a warning.
func (rtx *Context) abortOutputs() {
	rtx.mu.Lock()
	open := slices.Clone(rtx.outputs)
	rtx.mu.Unlock()
	for _, f := range open {
		if err := f.Abort(); err != nil {
			rtx.RecordWarning(err)
		}
	}
}

// renameOutput moves a finished temporary file onto its target.
func renameOutput(from, to string) error {
	return retryRename(os.Rename, from, to, renameRetryable, time.Sleep)
}

// retryRename renames, retrying up to five times with a growing pause (10ms to 160ms) while
// retryable says another program holds the file.
func retryRename(rename func(string, string) error, from, to string, retryable func(error) bool, sleep func(time.Duration)) error {
	pause := 10 * time.Millisecond
	err := rename(from, to)
	for range 5 {
		if err == nil || !retryable(err) {
			break
		}
		sleep(pause)
		pause *= 2
		err = rename(from, to)
	}
	return err
}
