package cfgedit

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// Format names a config file format, using the words a spec's config_files entry uses.
type Format string

// The formats Set and Unset edit.
const (
	YAML   Format = "yaml"
	JSON   Format = "json"
	JSONC  Format = "jsonc"
	TOML   Format = "toml"
	Dotenv Format = "dotenv"
)

var (
	// ErrRefused marks an edit the file's layout doesn't allow without changing other values
	// or guessing at structure, such as a path through an alias or an insert into a flow
	// mapping. The user edits such a file by hand.
	ErrRefused = errors.New("edit by hand")

	// ErrUnsupported marks a format cfgedit can't write.
	ErrUnsupported = errors.New("unsupported format")

	// ErrVerify marks an edit whose result didn't decode to the original document with only
	// the intended change. It is a bug in cfgedit, never in the file; nothing is returned.
	ErrVerify = errors.New("edit verification failed")
)

// RefusedError is an edit refused because of the file's layout. It matches [ErrRefused].
type RefusedError struct {
	Path   string // the dotted key path being edited
	Reason string // why, in a few words: "the path goes through an alias"
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("can't edit %q: %s; edit the file by hand", e.Path, e.Reason)
}

// Is reports whether target is [ErrRefused].
func (*RefusedError) Is(target error) bool { return target == ErrRefused }

func refused(path []string, format string, args ...any) error {
	return &RefusedError{Path: strings.Join(path, "."), Reason: fmt.Sprintf(format, args...)}
}

// Option adjusts an edit.
type Option func(*options)

type options struct {
	keep int
}

// KeepParents stops [Unset] from removing the first n segments of the path when the removal
// leaves them empty. An emptied kept mapping is written as {} so it still decodes as a mapping.
// Without it, every mapping the removal empties is removed too.
func KeepParents(n int) Option {
	return func(o *options) { o.keep = max(n, 0) }
}

// Set writes value at path in src, a file in the given format, and returns the edited bytes.
// path is the key's segments (deploy.timeout is ["deploy", "timeout"]; a dotenv key is one
// segment). A missing key is inserted with any missing parent mappings; an existing one has
// only its value replaced.
//
// value is a string, a bool, an integer, a float, or a list or string-keyed map of those.
// Strings are written plainly when that reads back as the same string, and quoted otherwise.
//
// Every byte outside the edited span is kept. A layout that can't be edited safely returns an
// error matching [ErrRefused]; TOML before support is built returns [ErrUnsupported].
func Set(src []byte, format Format, path []string, value any) ([]byte, error) {
	if !validPath(path) {
		return nil, fmt.Errorf("cfgedit: invalid key path %q", strings.Join(path, "."))
	}
	v, err := normalizeValue(value)
	if err != nil {
		return nil, fmt.Errorf("cfgedit: %q: %w", strings.Join(path, "."), err)
	}
	if format == Dotenv {
		// Every dotenv value is a string; compare what's written.
		if v, err = scalarText(v); err != nil {
			return nil, fmt.Errorf("cfgedit: %q: %w", strings.Join(path, "."), err)
		}
	}
	ed, err := editorFor(format)
	if err != nil {
		return nil, err
	}
	if loneCR(src) {
		return nil, refused(path, "the file ends lines with a lone carriage return")
	}
	before, err := decode(format, src)
	if err != nil {
		return nil, err
	}
	out, err := ed.set(src, path, v)
	if err != nil {
		return nil, err
	}
	setPath(before, path, v)
	return verify(format, src, out, before)
}

// Unset removes the key at path from src and returns the edited bytes. Comment lines above
// the key stay; a comment on the key's own line goes with it. Mappings the removal leaves empty
// are removed too, except the first n kept by [KeepParents]. Removing an absent key returns src
// unchanged.
func Unset(src []byte, format Format, path []string, opts ...Option) ([]byte, error) {
	if !validPath(path) {
		return nil, fmt.Errorf("cfgedit: invalid key path %q", strings.Join(path, "."))
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	ed, err := editorFor(format)
	if err != nil {
		return nil, err
	}
	if loneCR(src) {
		return nil, refused(path, "the file ends lines with a lone carriage return")
	}
	before, err := decode(format, src)
	if err != nil {
		return nil, err
	}
	out, err := ed.unset(src, path, o.keep)
	if err != nil {
		return nil, err
	}
	unsetPath(before, path, o.keep)
	return verify(format, src, out, before)
}

// validPath reports whether path has at least one segment and every segment is non-empty,
// valid UTF-8.
func validPath(path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, seg := range path {
		if seg == "" || !utf8.ValidString(seg) {
			return false
		}
	}
	return true
}

// editor is one format's splice editor. Both methods return src itself when nothing changes.
type editor interface {
	set(src []byte, path []string, v any) ([]byte, error)
	unset(src []byte, path []string, keep int) ([]byte, error)
}

func editorFor(format Format) (editor, error) {
	switch format {
	case YAML:
		return yamlEditor{}, nil
	case JSON:
		return jsonEditor{strict: true}, nil
	case JSONC:
		return jsonEditor{}, nil
	case Dotenv:
		return dotenvEditor{}, nil
	case TOML:
		return newTOMLEditor()
	}
	return nil, fmt.Errorf("cfgedit: format %q: %w", format, ErrUnsupported)
}

// normalizeValue converts a caller's value to the few shapes the renderers handle: string,
// bool, int64, uint64, float64, []any and map[string]any.
func normalizeValue(v any) (any, error) {
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) {
			return nil, errors.New("can't write a string that isn't valid UTF-8")
		}
		return x, nil
	case bool, int64, uint64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("can't write %v", x)
		}
		return x, nil
	case float32:
		return normalizeValue(float64(x))
	case nil:
		return nil, errors.New("can't write a null value")
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint(), nil
	case reflect.String:
		return normalizeValue(rv.String())
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			item, err := normalizeScalar(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("can't write a map keyed by %s", rv.Type().Key())
		}
		out := make(map[string]any, rv.Len())
		for it := rv.MapRange(); it.Next(); {
			if !utf8.ValidString(it.Key().String()) {
				return nil, errors.New("can't write a key that isn't valid UTF-8")
			}
			item, err := normalizeScalar(it.Value().Interface())
			if err != nil {
				return nil, err
			}
			out[it.Key().String()] = item
		}
		return out, nil
	}
	return nil, fmt.Errorf("can't write a value of type %T", v)
}

// normalizeScalar is normalizeValue for a list item or map value, which must be a scalar.
func normalizeScalar(v any) (any, error) {
	n, err := normalizeValue(v)
	if err != nil {
		return nil, err
	}
	switch n.(type) {
	case []any, map[string]any:
		return nil, errors.New("can't write a nested list or map")
	}
	return n, nil
}

// sortedKeys returns m's keys in order, so a written map is deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
