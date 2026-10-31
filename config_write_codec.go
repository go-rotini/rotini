package rotini

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-rotini/rotini/internal/cfgedit"
)

// set writes value at path: in place for a built-in format, else, for a [Codec] format with
// the encoder enc, by rewriting the file.
func (t configFileTarget) set(format cfgedit.Format, enc *codecEntry, path []string, value any) ([]byte, error) {
	if enc != nil {
		return t.rewrite(enc, path, value, true, 0)
	}
	return cfgedit.Set(t.src, format, path, value) //nolint:wrapcheck // reported by editError
}

// unset removes path, keeping the first keep segments' mappings, as set writes.
func (t configFileTarget) unset(format cfgedit.Format, enc *codecEntry, path []string, keep int) ([]byte, error) {
	if enc != nil {
		return t.rewrite(enc, path, nil, false, keep)
	}
	return cfgedit.Unset(t.src, format, path, cfgedit.KeepParents(keep)) //nolint:wrapcheck // reported by editError
}

// rewrite is a config write to a file in a [Codec] format with an encoder: the file is decoded
// with c, value is set at path (or path is removed when set is false, emptied mappings with it
// except the first keep segments), and the whole tree is encoded again. It returns t.src itself
// when the tree doesn't change, so the file isn't touched.
func (t configFileTarget) rewrite(c *codecEntry, path []string, value any, set bool, keep int) ([]byte, error) {
	doc := map[string]any{}
	if t.exists {
		var err error
		if doc, err = c.codec().Decode(stripBOM(t.src)); err != nil {
			return nil, fileParseError(t.path, err)
		}
	}
	var changed bool
	if set {
		var err error
		if changed, err = setTreePath(doc, path, value); err != nil {
			return nil, err
		}
	} else {
		changed = unsetTreePath(doc, path, keep)
	}
	if !changed {
		return t.src, nil
	}
	out, err := c.codec().Encode(doc)
	if err != nil {
		return nil, &encodeError{path: strings.Join(path, "."), err: err}
	}
	if _, err := c.codec().Decode(stripBOM(out)); err != nil {
		return nil, fmt.Errorf("%w: the %s encoder wrote a document its decoder can't read: %w", cfgedit.ErrVerify, c.name, err)
	}
	return out, nil
}

// setTreePath sets doc's value at path to v, adding missing mappings on the way, and reports
// whether doc changed. A path through a value that isn't a mapping is refused.
func setTreePath(doc map[string]any, path []string, v any) (bool, error) {
	m := doc
	for i, seg := range path[:len(path)-1] {
		switch next := m[seg].(type) {
		case map[string]any:
			m = next
		case nil:
			if _, held := m[seg]; held {
				return false, &cfgedit.RefusedError{Path: strings.Join(path, "."), Reason: strings.Join(path[:i+1], ".") + " is null, not a mapping"}
			}
			sub := map[string]any{}
			m[seg] = sub
			m = sub
		default:
			return false, &cfgedit.RefusedError{Path: strings.Join(path, "."), Reason: strings.Join(path[:i+1], ".") + " holds a value, not a mapping"}
		}
	}
	last := path[len(path)-1]
	if old, held := m[last]; held && reflect.DeepEqual(old, v) {
		return false, nil
	}
	m[last] = v
	return true, nil
}

// unsetTreePath removes path from doc, then each mapping on the path the removal leaves empty,
// deepest first, except the first keep, and reports whether doc changed.
func unsetTreePath(doc map[string]any, path []string, keep int) bool {
	chain := []map[string]any{doc}
	for _, seg := range path[:len(path)-1] {
		next, ok := chain[len(chain)-1][seg].(map[string]any)
		if !ok {
			return false
		}
		chain = append(chain, next)
	}
	last := chain[len(chain)-1]
	if _, ok := last[path[len(path)-1]]; !ok {
		return false
	}
	delete(last, path[len(path)-1])
	for i := len(chain) - 1; i > 0 && i > keep && len(chain[i]) == 0; i-- {
		delete(chain[i-1], path[i-1])
	}
	return true
}

// encodeError is a value a [Codec] format's encoder can't write. It matches [cfgedit.ErrValue].
type encodeError struct {
	path string
	err  error
}

func (e *encodeError) Error() string      { return fmt.Sprintf("can't write %q: %v", e.path, e.err) }
func (e *encodeError) Unwrap() error      { return e.err }
func (*encodeError) Is(target error) bool { return target == cfgedit.ErrValue }
