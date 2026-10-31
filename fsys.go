package rotini

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// WriteFS is an [fs.FS] a run can also write through: [CreateOutput] and the config writes
// ([SetConfigValue] and [UnsetConfigValue]) create, replace and remove files with it. Names
// are [fs.FS] names, as [Program.WithFS] maps them.
//
// A file OpenFile returns may also have a Chmod(fs.FileMode) error method, which rotini calls to
// give a replacement the mode of the file it replaces, and a Sync() error method, which a config
// write calls before the rename. Both are optional, and an [*os.File] has both.
type WriteFS interface {
	fs.FS
	// OpenFile opens name for writing, with os.OpenFile's flags: O_WRONLY to write an existing
	// file in place (a device or a named pipe), or O_WRONLY|O_CREATE|O_EXCL to create a new
	// file with perm, which must fail with [fs.ErrExist] when name exists.
	OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error)
	// MkdirAll creates directory name and any parents it lacks with perm, as os.MkdirAll does.
	MkdirAll(name string, perm fs.FileMode) error
	// Rename moves oldname to newname, replacing a file there, as os.Rename does.
	Rename(oldname, newname string) error
	// Remove removes the file name.
	Remove(name string) error
}

// OSFS returns the operating system's file system as a [WriteFS] whose names are [Program.WithFS]
// names: etc/app/config.yaml is /etc/app/config.yaml (on Windows, C:/app/config.yaml is
// C:\app\config.yaml). It is what a run uses when none is set, and what a file system that
// adds to the real one, such as a [LayerFS], is built over. Hide its write methods to make it
// read-only:
//
//	p.WithFS(struct{ fs.FS }{rotini.OSFS()})
//
// It also implements [fs.StatFS], [fs.ReadDirFS], [fs.ReadFileFS] and [fs.ReadLinkFS].
func OSFS() WriteFS { return osFS{} }

// osFS is [OSFS]: each name is the OS path osPathOf maps it to.
type osFS struct{}

func (osFS) Open(name string) (fs.File, error) {
	p, err := osPathOf("open", name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err //nolint:wrapcheck // os names the path
	}
	return f, nil
}

func (osFS) Stat(name string) (fs.FileInfo, error) {
	p, err := osPathOf("stat", name)
	if err != nil {
		return nil, err
	}
	return os.Stat(p) //nolint:wrapcheck // os names the path
}

func (osFS) Lstat(name string) (fs.FileInfo, error) {
	p, err := osPathOf("lstat", name)
	if err != nil {
		return nil, err
	}
	return os.Lstat(p) //nolint:wrapcheck // os names the path
}

func (osFS) ReadLink(name string) (string, error) {
	p, err := osPathOf("readlink", name)
	if err != nil {
		return "", err
	}
	return os.Readlink(p) //nolint:wrapcheck // os names the path
}

func (osFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := osPathOf("readdir", name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(p) //nolint:wrapcheck // os names the path
}

func (osFS) ReadFile(name string) ([]byte, error) {
	p, err := osPathOf("open", name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p) //nolint:wrapcheck // os names the path
}

func (osFS) OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error) {
	p, err := osPathOf("open", name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err //nolint:wrapcheck // os names the path
	}
	return f, nil
}

func (osFS) MkdirAll(name string, perm fs.FileMode) error {
	p, err := osPathOf("mkdir", name)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, perm) //nolint:wrapcheck // os names the path
}

func (osFS) Rename(oldname, newname string) error {
	from, err := osPathOf("rename", oldname)
	if err != nil {
		return err
	}
	to, err := osPathOf("rename", newname)
	if err != nil {
		return err
	}
	return os.Rename(from, to) //nolint:wrapcheck // os names the paths
}

func (osFS) Remove(name string) error {
	p, err := osPathOf("remove", name)
	if err != nil {
		return err
	}
	return os.Remove(p) //nolint:wrapcheck // os names the path
}

// osPathOf is the OS path of an [fs.FS] name, the inverse of fsNameOf: "etc/x" is "/etc/x",
// and on Windows "C:/x" is C:\x and "host/share/x" is \\host\share\x.
func osPathOf(op, name string) (string, error) {
	if !fs.ValidPath(name) || (runtime.GOOS == "windows" && name == ".") {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if runtime.GOOS != "windows" {
		if name == "." {
			return "/", nil
		}
		return "/" + name, nil
	}
	first, rest, _ := strings.Cut(name, "/")
	if len(first) == 2 && first[1] == ':' {
		return first + `\` + filepath.FromSlash(rest), nil
	}
	return `\\` + filepath.FromSlash(name), nil
}

// fsNameOf is the [fs.FS] name of the absolute, clean OS path p: its slash form without the
// leading separator, "." for the root. A Windows volume is the first element: C:\x is "C:/x",
// \\host\share\x is "host/share/x".
func fsNameOf(p string) string {
	vol := filepath.VolumeName(p)
	rest := strings.TrimLeft(filepath.ToSlash(p[len(vol):]), "/")
	vol = strings.TrimLeft(filepath.ToSlash(vol), "/")
	switch {
	case vol == "" && rest == "":
		return "."
	case vol == "":
		return rest
	case rest == "":
		return vol
	}
	return vol + "/" + rest
}

// defaultFS is the file system of a run that sets none; nil is the operating system's, read
// and written through os directly. Tests replace it to run every file access through an FS.
var defaultFS fs.FS

// withFS returns a copy of v reading and writing files through fsys; nil, or [OSFS], is the
// operating system's.
func (v *osView) withFS(fsys fs.FS) *osView {
	out := &osView{foldCase: runtime.GOOS == "windows"}
	if v != nil {
		*out = *v
	}
	if _, isOS := fsys.(osFS); isOS {
		fsys = nil
	}
	out.fsys = fsys
	return out
}

// fileSystem is the run's file system; nil for the operating system's, which the file helpers
// below reach through os directly.
func (v *osView) fileSystem() fs.FS {
	if v != nil && v.fsys != nil {
		return v.fsys
	}
	return defaultFS
}

// The file helpers take an OS path, absolute or relative to the process working directory, as
// the os function of the same name does. With no FS they are that function; with one, the path
// is made absolute and read or written as its [fs.FS] name, and an [*fs.PathError] the FS
// returns names the path again.

// fsName maps p to fsys's name for it.
func fsName(op, p string) (string, error) {
	abs := p
	if !filepath.IsAbs(p) {
		var err error
		if abs, err = filepath.Abs(p); err != nil {
			return "", &fs.PathError{Op: op, Path: p, Err: err}
		}
	}
	name := fsNameOf(filepath.Clean(abs))
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: p, Err: fs.ErrInvalid}
	}
	return name, nil
}

// relabel makes an [*fs.PathError] an FS returned name the OS path p, as os's errors do.
func relabel(err error, p string) error {
	if pe, ok := err.(*fs.PathError); ok { //nolint:errorlint // only the FS's own error names its name
		return &fs.PathError{Op: pe.Op, Path: p, Err: pe.Err}
	}
	return err
}

// open is os.Open through the run's file system.
func (v *osView) open(p string) (fs.File, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		f, err := os.Open(p)
		if err != nil {
			return nil, err //nolint:wrapcheck // os names the path
		}
		return f, nil
	}
	name, err := fsName("open", p)
	if err != nil {
		return nil, err
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, relabel(err, p)
	}
	return f, nil
}

// stat is os.Stat through the run's file system.
func (v *osView) stat(p string) (fs.FileInfo, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return os.Stat(p) //nolint:wrapcheck // os names the path
	}
	name, err := fsName("stat", p)
	if err != nil {
		return nil, err
	}
	info, err := fs.Stat(fsys, name)
	return info, relabel(err, p)
}

// lstat is os.Lstat through the run's file system: [fs.Lstat], which is stat for an FS that
// has no symbolic links.
func (v *osView) lstat(p string) (fs.FileInfo, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return os.Lstat(p) //nolint:wrapcheck // os names the path
	}
	name, err := fsName("lstat", p)
	if err != nil {
		return nil, err
	}
	info, err := fs.Lstat(fsys, name)
	return info, relabel(err, p)
}

// readDir is os.ReadDir through the run's file system.
func (v *osView) readDir(p string) ([]fs.DirEntry, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return os.ReadDir(p) //nolint:wrapcheck // os names the path
	}
	name, err := fsName("readdir", p)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, name)
	return entries, relabel(err, p)
}

// readFile is os.ReadFile through the run's file system.
func (v *osView) readFile(p string) ([]byte, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return os.ReadFile(p) //nolint:wrapcheck // os names the path
	}
	name, err := fsName("open", p)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(fsys, name)
	return data, relabel(err, p)
}

// maxLinks bounds the symbolic links evalSymlinks follows, as the operating system does.
const maxLinks = 255

// evalSymlinks is filepath.EvalSymlinks through the run's file system. Over an FS, only the
// last element is followed, link by link through [fs.ReadLink]: a relative link is relative to
// the directory holding it, and an absolute one is an OS path.
func (v *osView) evalSymlinks(p string) (string, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return filepath.EvalSymlinks(p) //nolint:wrapcheck // os names the path
	}
	at := p
	for range maxLinks {
		name, err := fsName("lstat", at)
		if err != nil {
			return "", err
		}
		info, err := fs.Lstat(fsys, name)
		if err != nil {
			return "", relabel(err, at)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return at, nil
		}
		link, err := fs.ReadLink(fsys, name)
		if err != nil {
			return "", relabel(err, at)
		}
		link = filepath.FromSlash(link)
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(at), link)
		}
		at = filepath.Clean(link)
	}
	return "", &fs.PathError{Op: "lstat", Path: p, Err: errTooManyLinks}
}

var errTooManyLinks error = constError("too many levels of symbolic links")

// readOnlyError is the error for a write to a file system that is not a [WriteFS]. It
// matches [errors.ErrUnsupported].
type readOnlyError struct{}

func (readOnlyError) Error() string        { return "the program's file system is read-only" }
func (readOnlyError) Is(target error) bool { return target == errors.ErrUnsupported }

// writable reports whether the run's file system can be written: the operating system's, or a
// [WriteFS].
func (v *osView) writable() bool {
	fsys := v.fileSystem()
	if fsys == nil {
		return true
	}
	_, ok := fsys.(WriteFS)
	return ok
}

// writeFS is the run's [WriteFS] and its name for p, or nil for the operating system's.
func (v *osView) writeFS(op, p string) (WriteFS, string, error) {
	fsys := v.fileSystem()
	if fsys == nil {
		return nil, "", nil
	}
	wfs, ok := fsys.(WriteFS)
	if !ok {
		return nil, "", &fs.PathError{Op: op, Path: p, Err: readOnlyError{}}
	}
	name, err := fsName(op, p)
	if err != nil {
		return nil, "", err
	}
	return wfs, name, nil
}

// openFile is os.OpenFile through the run's file system. The file is an [*os.File] without one.
func (v *osView) openFile(p string, flag int, perm fs.FileMode) (io.WriteCloser, error) {
	wfs, name, err := v.writeFS("open", p)
	if err != nil {
		return nil, err
	}
	if wfs == nil {
		f, err := os.OpenFile(p, flag, perm)
		if err != nil {
			return nil, err //nolint:wrapcheck // os names the path
		}
		return f, nil
	}
	f, err := wfs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, relabel(err, p)
	}
	return f, nil
}

// mkdirAll is os.MkdirAll through the run's file system.
func (v *osView) mkdirAll(p string, perm fs.FileMode) error {
	wfs, name, err := v.writeFS("mkdir", p)
	if err != nil {
		return err
	}
	if wfs == nil {
		return os.MkdirAll(p, perm) //nolint:wrapcheck // os names the path
	}
	return relabel(wfs.MkdirAll(name, perm), p)
}

// rename is os.Rename through the run's file system.
func (v *osView) rename(from, to string) error {
	wfs, fromName, err := v.writeFS("rename", from)
	if err != nil {
		return err
	}
	if wfs == nil {
		return os.Rename(from, to) //nolint:wrapcheck // os names the paths
	}
	toName, err := fsName("rename", to)
	if err != nil {
		return err
	}
	return relabel(wfs.Rename(fromName, toName), from)
}

// remove is os.Remove through the run's file system.
func (v *osView) remove(p string) error {
	wfs, name, err := v.writeFS("remove", p)
	if err != nil {
		return err
	}
	if wfs == nil {
		return os.Remove(p) //nolint:wrapcheck // os names the path
	}
	return relabel(wfs.Remove(name), p)
}

// chmodFile gives a file openFile created the mode perm, when it can be given one.
func chmodFile(f io.Writer, perm fs.FileMode) error {
	if c, ok := f.(interface{ Chmod(mode fs.FileMode) error }); ok {
		return c.Chmod(perm) //nolint:wrapcheck // wrapped by the caller
	}
	return nil
}

// syncWritten flushes a file the run's file system opened to stable storage, when it can be
// flushed.
func syncWritten(f any) error {
	if of, ok := f.(*os.File); ok {
		return syncFile(of)
	}
	if s, ok := f.(interface{ Sync() error }); ok {
		return s.Sync() //nolint:wrapcheck // wrapped by the caller
	}
	return nil
}
