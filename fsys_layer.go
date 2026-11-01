package rotini

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// LayerFS returns a file system that shows lower's files at directory dir of upper, under
// upper's own: a name at or below dir is read from upper when upper has it, and from lower
// otherwise, and listing a directory there lists both. dir itself is listed in its parent
// directory even when only lower makes it exist. Every other name is upper's. dir is an OS
// path, made absolute against the process working directory, and need not exist in upper.
// Names are [Program.WithFS] names; lower's are relative to dir. A name is matched against
// dir as the operating system compares paths: without regard to case on Windows, and exactly
// elsewhere, macOS included, whose file systems may or may not fold case.
//
// When upper is a [WriteFS] the result is one too, and every write goes to upper: a file
// written over one of lower's lands in upper, and lower is never changed. Such a file is
// created with the mode rotini gives a new file (0644, or 0600 for a configuration file that
// may hold a secret), not lower's, whose modes are often meaningless (0444 in an [embed.FS], 0
// in a MapFS); only a file upper itself holds keeps its own. A file created in a directory
// only lower has, or in dir when upper lacks it, gets the directories it needs in upper first,
// with mode 0755.
//
// Over [OSFS], it puts files embedded in the binary beneath the real ones, so a configuration
// file the program ships is read until the user puts their own at its path:
//
//	//go:embed defaults
//	var defaults embed.FS
//
//	sub, _ := fs.Sub(defaults, "defaults")
//	p.WithFS(rotini.LayerFS(rotini.OSFS(), "/etc/app", sub))
func LayerFS(upper fs.FS, dir string, lower fs.FS) fs.FS {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	l := layerFS{upper: upper, lower: lower, mount: fsNameOf(filepath.Clean(dir)), fold: runtime.GOOS == "windows"}
	if w, ok := upper.(WriteFS); ok {
		return writeLayerFS{layerFS: l, w: w}
	}
	return l
}

// layerFS is [LayerFS] over an upper file system that can't be written.
type layerFS struct {
	upper, lower fs.FS
	mount        string // dir's name in upper
	fold         bool   // names match the mount without regard to case
}

// lowerName is name's name in lower, and whether name is at or below the mount.
func (l layerFS) lowerName(name string) (string, bool) {
	switch {
	case l.mount == ".":
		return name, true
	case l.same(name, l.mount):
		return ".", true
	case len(name) > len(l.mount) && name[len(l.mount)] == '/' && l.same(name[:len(l.mount)], l.mount):
		return name[len(l.mount)+1:], true
	}
	return "", false
}

// same reports whether names a and b match, as the mount compares them.
func (l layerFS) same(a, b string) bool { return a == b || (l.fold && strings.EqualFold(a, b)) }

// mountEntry is the mount's entry in directory name when name is the mount's parent and only
// lower makes the mount exist, and whether it is.
func (l layerFS) mountEntry(name string) (fs.DirEntry, bool) {
	if l.mount == "." || !l.same(path.Dir(l.mount), name) {
		return nil, false
	}
	if _, err := fs.Stat(l.upper, l.mount); !errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	info, err := fs.Stat(l.lower, ".")
	if err != nil || !info.IsDir() {
		return nil, false
	}
	return fs.FileInfoToDirEntry(mountInfo{info, path.Base(l.mount)}), true
}

// mountInfo is lower's root, as the mount names it.
type mountInfo struct {
	fs.FileInfo

	name string
}

func (m mountInfo) Name() string      { return m.name }
func (m mountInfo) Mode() fs.FileMode { return fs.ModeDir | m.FileInfo.Mode().Perm() }

// layered is a file system that shows files from a layer its writes don't reach: a [LayerFS].
type layered interface {
	// inUpper reports whether the layer writes go to holds name itself.
	inUpper(name string) bool
}

func (l layerFS) inUpper(name string) bool {
	_, err := fs.Lstat(l.upper, name)
	return err == nil
}

// fromLower makes an [*fs.PathError] from lower name name, as upper's do.
func fromLower(err error, name string) error {
	if pe, ok := err.(*fs.PathError); ok { //nolint:errorlint // only lower's own error names its name
		return &fs.PathError{Op: pe.Op, Path: name, Err: pe.Err}
	}
	return err
}

func (l layerFS) Open(name string) (fs.File, error) {
	f, err := l.upper.Open(name)
	if lname, ok := l.lowerName(name); ok && errors.Is(err, fs.ErrNotExist) {
		f, err = l.lower.Open(lname)
		return f, fromLower(err, name)
	}
	return f, err //nolint:wrapcheck // upper's own error
}

func (l layerFS) Stat(name string) (fs.FileInfo, error) {
	info, err := fs.Stat(l.upper, name)
	if lname, ok := l.lowerName(name); ok && errors.Is(err, fs.ErrNotExist) {
		info, err = fs.Stat(l.lower, lname)
		return info, fromLower(err, name)
	}
	return info, err //nolint:wrapcheck // upper's own error
}

func (l layerFS) Lstat(name string) (fs.FileInfo, error) {
	info, err := fs.Lstat(l.upper, name)
	if lname, ok := l.lowerName(name); ok && errors.Is(err, fs.ErrNotExist) {
		info, err = fs.Lstat(l.lower, lname)
		return info, fromLower(err, name)
	}
	return info, err //nolint:wrapcheck // upper's own error
}

func (l layerFS) ReadLink(name string) (string, error) {
	link, err := fs.ReadLink(l.upper, name)
	if lname, ok := l.lowerName(name); ok && errors.Is(err, fs.ErrNotExist) {
		link, err = fs.ReadLink(l.lower, lname)
		return link, fromLower(err, name)
	}
	return link, err //nolint:wrapcheck // upper's own error
}

func (l layerFS) ReadFile(name string) ([]byte, error) {
	data, err := fs.ReadFile(l.upper, name)
	if lname, ok := l.lowerName(name); ok && errors.Is(err, fs.ErrNotExist) {
		data, err = fs.ReadFile(l.lower, lname)
		return data, fromLower(err, name)
	}
	return data, err //nolint:wrapcheck // upper's own error
}

// ReadDir lists a directory of upper and, at or below the mount, lower's directory of that
// name too, sorted by name: an entry in both is upper's. The mount's parent lists the mount.
func (l layerFS) ReadDir(name string) ([]fs.DirEntry, error) {
	up, uerr := fs.ReadDir(l.upper, name)
	lname, ok := l.lowerName(name)
	if !ok || (uerr != nil && !errors.Is(uerr, fs.ErrNotExist)) {
		if e, ok := l.mountEntry(name); ok && uerr == nil {
			up = sortedEntries(append(up, e))
		}
		return up, uerr //nolint:wrapcheck // upper's own error
	}
	low, lerr := fs.ReadDir(l.lower, lname)
	if lerr != nil && !errors.Is(lerr, fs.ErrNotExist) {
		return nil, fromLower(lerr, name)
	}
	upMissing, lowMissing := uerr != nil, lerr != nil
	switch {
	case upMissing && lowMissing:
		return nil, uerr //nolint:wrapcheck // upper's own error
	case lowMissing:
		return up, nil //nolint:nilerr // only upper has the directory
	case upMissing:
		return low, nil //nolint:nilerr // only lower has the directory
	}
	for _, e := range low {
		if !slices.ContainsFunc(up, func(u fs.DirEntry) bool { return u.Name() == e.Name() }) {
			up = append(up, e)
		}
	}
	return sortedEntries(up), nil
}

// sortedEntries sorts entries by name, as [fs.ReadDir] returns them.
func sortedEntries(entries []fs.DirEntry) []fs.DirEntry {
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries
}

// writeLayerFS is [LayerFS] over a [WriteFS], which every write goes to.
type writeLayerFS struct {
	layerFS

	w WriteFS
}

// OpenFile opens name in upper. A file it creates in a directory upper lacks but the layers
// show, through the mount, gets that directory in upper first.
func (l writeLayerFS) OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error) {
	if flag&os.O_CREATE != 0 && l.lowerOnlyDir(path.Dir(name)) {
		if err := l.w.MkdirAll(path.Dir(name), 0o755); err != nil {
			return nil, err //nolint:wrapcheck // upper's own error
		}
	}
	return l.w.OpenFile(name, flag, perm) //nolint:wrapcheck // upper's own error
}

// lowerOnlyDir reports whether dir is a directory the layers show at or below the mount that
// upper lacks.
func (l writeLayerFS) lowerOnlyDir(dir string) bool {
	if _, under := l.lowerName(dir); !under {
		return false
	}
	if _, err := fs.Stat(l.upper, dir); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	info, err := l.Stat(dir)
	return err == nil && info.IsDir()
}

func (l writeLayerFS) MkdirAll(name string, perm fs.FileMode) error {
	return l.w.MkdirAll(name, perm) //nolint:wrapcheck // upper's own error
}

func (l writeLayerFS) Rename(oldname, newname string) error {
	return l.w.Rename(oldname, newname) //nolint:wrapcheck // upper's own error
}

func (l writeLayerFS) Remove(name string) error {
	return l.w.Remove(name) //nolint:wrapcheck // upper's own error
}
