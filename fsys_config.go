package rotini

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-rotini/recon"
)

// fileDoc is an opened configuration file's source: a [*recon.FileSource] when the run reads
// the operating system's files, an [*fsFileSource] when it reads through a [Program.WithFS]
// file system.
type fileDoc interface {
	recon.Source
	Path() string
	Format() string
	Document() (map[string]any, bool)
}

// fsFileSource is a configuration file read through the run's file system, standing in for the
// [*recon.FileSource] recon builds for the operating system's: the same name, keys, path and
// errors.
type fsFileSource struct {
	*recon.MapSource

	view  *osView
	path  string
	codec recon.Codec
	doc   map[string]any // the decoded content
	read  bool           // the file had content to decode: it was neither missing nor empty
}

// Path is the absolute path the file was read from.
func (s *fsFileSource) Path() string { return s.path }

// Format is the name of the codec that decoded the file.
func (s *fsFileSource) Format() string { return s.codec.Name() }

// Document returns a copy of the file's document and true, or nil and false when the file was
// missing or empty.
func (s *fsFileSource) Document() (map[string]any, bool) {
	if !s.read {
		return nil, false
	}
	if s.doc == nil {
		return map[string]any{}, true
	}
	m, _ := cloneValue(s.doc).(map[string]any)
	return m, true
}

// cloneValue copies a decoded document's value deeply, as recon copies a file source's.
func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	case []string:
		return slices.Clone(x)
	case map[string]string:
		return maps.Clone(x)
	}
	return v
}

// sourceView is the view a configuration file source reads through: the run's for one read
// through a file system, nil (the operating system's) otherwise.
func sourceView(src recon.Source) *osView {
	if s, ok := src.(*fsFileSource); ok {
		return s.view
	}
	return nil
}

// newFSFileSource is [newFileSource] reading path through view's file system: the codec is c,
// else the wired codec format names, else the one for path's extension. An optional file that
// doesn't exist is empty.
func newFSFileSource(view *osView, feats *featureSet, path string, c *codecEntry, format string, optional bool) (recon.Source, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("recon: resolve %q: %w", path, err)
	}
	codec, err := fsCodec(feats.reconCodecs(), c, format, abs)
	if err != nil {
		return nil, err
	}
	data, err := readConfigCapped(view, abs)
	switch {
	case err != nil && configMissing(err) && optional:
		data = nil
	case err != nil && configMissing(err):
		return nil, fmt.Errorf("recon: file %q: %w", abs, fs.ErrNotExist)
	case err != nil:
		return nil, fmt.Errorf("recon: read %q: %w", abs, err)
	}
	data = stripBOM(data)
	name := filepath.Base(abs)
	var doc map[string]any
	if len(data) > 0 {
		if doc, err = codec.Decode(data); err != nil {
			return nil, configParseError(name, abs, err)
		}
	}
	return &fsFileSource{MapSource: recon.NewMapSource(name, doc), view: view, path: abs, codec: codec, doc: doc, read: len(data) > 0}, nil
}

// fsCodec picks the codec a configuration file decodes with, as recon picks it for a file
// source: c, else format's, else the one for path's extension.
func fsCodec(codecs *recon.Codecs, c *codecEntry, format, path string) (recon.Codec, error) {
	if c != nil {
		return c.codec(), nil
	}
	if codecs == nil {
		codecs = recon.NewCodecs()
	}
	if format != "" {
		codec, ok := codecs.ByName(format)
		if !ok {
			return nil, fmt.Errorf("%w: no codec registered for format %q", recon.ErrUnsupportedFormat, format)
		}
		return codec, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	codec, ok := codecs.ByExtension(ext)
	if !ok {
		return nil, fmt.Errorf("%w: no codec for extension %q (path %q)", recon.ErrUnsupportedFormat, ext, path)
	}
	return codec, nil
}

// readConfigCapped reads a configuration file through view's file system, refusing one larger
// than [recon.MaxFileSize] as recon does.
func readConfigCapped(view *osView, path string) ([]byte, error) {
	f, err := view.open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Mode().IsRegular() && info.Size() > recon.MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: path, Err: recon.ErrFileTooLarge}
	}
	data, err := io.ReadAll(io.LimitReader(f, recon.MaxFileSize+1))
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	if int64(len(data)) > recon.MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: path, Err: recon.ErrFileTooLarge}
	}
	return data, nil
}

// configParseError is the [*recon.ParseError] recon returns for a file source whose content
// doesn't decode: the message and position the codec reports.
func configParseError(name, path string, err error) *recon.ParseError {
	pe := &recon.ParseError{Source: name, Path: path, Msg: err.Error(), Cause: err}
	if inner, ok := asError[*recon.ParseError](err); ok && inner.Msg != "" {
		pe.Msg = inner.Msg
	} else if e, ok := asError[recon.PositionError](err); ok {
		pe.Msg = e.Message()
	}
	if line, col, ok := parsePosition(err); ok {
		pe.Position = recon.Position{Line: line, Column: col}
	}
	return pe
}

// configMissing reports whether err says a configuration file, or a directory on its path,
// doesn't exist, as recon reads it.
func configMissing(err error) bool { return pathMissing(err) || errors.Is(err, syscall.ENOTDIR) }
