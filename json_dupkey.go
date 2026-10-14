package rotini

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/go-rotini/jsonc"
)

// jsonDupKeyLinear is how many keys an object holds before its keys are looked up in a set.
const jsonDupKeyLinear = 16

// jsonKeyFrame is one open object or array of the document being scanned.
type jsonKeyFrame struct {
	obj     bool
	wantKey bool
	keys    [][]byte
	set     map[string]struct{}
	pending *jsonc.DuplicateKeyError
}

// jsonDuplicateKey finds a key given twice in one object of data, which must be valid JSON, and
// reports it as jsonc does: keys compare as encoding/json decodes them (escapes decoded, invalid
// UTF-8 as U+FFFD), and a repeated key is reported once its value has been read, so a repeat
// inside that value comes first. It returns nil when there is none.
func jsonDuplicateKey(data []byte) *jsonc.DuplicateKeyError {
	var stack []jsonKeyFrame
	top := func() *jsonKeyFrame {
		if len(stack) == 0 {
			return nil
		}
		return &stack[len(stack)-1]
	}
	for i := 0; i < len(data); i++ {
		switch c := data[i]; c {
		case ' ', '\t', '\r', '\n', ':':
		case '{', '[':
			if n := len(stack); n < cap(stack) {
				stack = stack[:n+1]
				f := &stack[n]
				f.obj, f.wantKey, f.keys, f.set, f.pending = c == '{', c == '{', f.keys[:0], nil, nil
			} else {
				stack = append(stack, jsonKeyFrame{obj: c == '{', wantKey: c == '{'})
			}
		case '}', ']':
			if f := top(); f.pending != nil {
				return f.pending
			}
			stack = stack[:len(stack)-1]
		case ',':
			if f := top(); f.obj {
				if f.pending != nil {
					return f.pending
				}
				f.wantKey = true
			}
		case '"':
			start := i
			i = jsonStringEnd(data, i)
			if f := top(); f != nil && f.wantKey {
				f.key(data, start, i)
			}
		default:
			for i+1 < len(data) && !jsonTokenEnd(data[i+1]) {
				i++
			}
		}
	}
	return nil
}

// jsonStringEnd is the index of the quote that closes the string opening at data[start].
func jsonStringEnd(data []byte, start int) int {
	i := start + 1
	for data[i] != '"' {
		if data[i] == '\\' {
			i++
		}
		i++
	}
	return i
}

// key records the object key quoted at data[start:end+1], noting the first one given twice.
func (f *jsonKeyFrame) key(data []byte, start, end int) {
	f.wantKey = false
	key := data[start+1 : end]
	if bytes.IndexByte(key, '\\') >= 0 || !utf8.Valid(key) {
		var s string
		if json.Unmarshal(data[start:end+1], &s) == nil {
			key = []byte(s)
		}
	}
	if f.pending == nil && f.seen(key) {
		line, col := jsonPosition(data[:start])
		f.pending = &jsonc.DuplicateKeyError{Key: string(key), Pos: jsonc.Position{Line: line, Column: col, Offset: start}}
	}
}

// jsonTokenEnd reports whether c ends a number or a literal.
func jsonTokenEnd(c byte) bool {
	switch c {
	case ',', ']', '}', ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

// seen records key in the object and reports whether it was already there.
func (f *jsonKeyFrame) seen(key []byte) bool {
	if f.set != nil {
		if _, ok := f.set[string(key)]; ok {
			return true
		}
		f.set[string(key)] = struct{}{}
		return false
	}
	for _, k := range f.keys {
		if bytes.Equal(k, key) {
			return true
		}
	}
	f.keys = append(f.keys, key)
	if len(f.keys) > jsonDupKeyLinear {
		f.set = make(map[string]struct{}, 2*len(f.keys))
		for _, k := range f.keys {
			f.set[string(k)] = struct{}{}
		}
	}
	return false
}

// jsonPosition is the 1-based line and byte column just past prefix, counting a newline, or a
// carriage return not followed by one, as a line break, as jsonc positions do.
func jsonPosition(prefix []byte) (line, col int) {
	line, col = 1, 1
	for i, c := range prefix {
		switch {
		case c == '\n', c == '\r' && (i+1 >= len(prefix) || prefix[i+1] != '\n'):
			line, col = line+1, 1
		default:
			col++
		}
	}
	return line, col
}
