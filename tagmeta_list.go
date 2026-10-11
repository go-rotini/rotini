package rotini

import (
	"reflect"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// tagList reads the list tag key: nil when it is absent or malformed.
func tagList(tag reflect.StructTag, key string) []string {
	v, ok := tag.Lookup(key)
	if !ok {
		return nil
	}
	list, _ := splitTagList(v)
	return list
}

// tagMap reads the map tag key: nil when it is absent or malformed.
func tagMap(tag reflect.StructTag, key string) map[string]string {
	v, ok := tag.Lookup(key)
	if !ok {
		return nil
	}
	m, _ := splitTagPairs(v)
	return m
}

// splitTagList splits a list tag's value into its unescaped items; "" is one empty item. ok is
// false for a "\" that escapes anything but "\", "," or "=".
func splitTagList(s string) ([]string, bool) {
	var out []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if !tagEscape(s, i) {
				return nil, false
			}
			i++
			b.WriteByte(s[i])
		case ',':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(out, b.String()), true
}

// splitTagPairs splits a map tag's value into its unescaped key=value pairs, each split at its
// first unescaped "="; "" is no pairs, and a later pair for a key wins. ok is false for a bad
// escape, a pair with no unescaped "=", or an empty key.
func splitTagPairs(s string) (map[string]string, bool) {
	out := map[string]string{}
	if s == "" {
		return out, true
	}
	var b strings.Builder
	eq := -1 // where the value starts in b, once the pair's "=" is read
	pair := func() bool {
		if eq <= 0 {
			return false
		}
		text := b.String()
		out[text[:eq]] = text[eq:]
		b.Reset()
		eq = -1
		return true
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			if !tagEscape(s, i) {
				return nil, false
			}
			i++
			b.WriteByte(s[i])
		case c == ',':
			if !pair() {
				return nil, false
			}
		case c == '=' && eq < 0:
			eq = b.Len()
		default:
			b.WriteByte(c)
		}
	}
	if !pair() {
		return nil, false
	}
	return out, true
}

// tagEscape reports whether the "\" at s[i] escapes a "\", "," or "=".
func tagEscape(s string, i int) bool {
	return i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == ',' || s[i+1] == '=')
}

// jsonStrings reads a JSON array of strings the way encoding/json decodes one into a nil
// []string: out is what the slice holds afterwards and ok whether decoding reported no error.
// Text that is not JSON leaves it nil; null is nil; an element that is null, or of another
// type, decodes as "" (and is an error unless null).
func jsonStrings(s string) (out []string, ok bool) {
	r := jsonReader{s: s}
	r.space()
	switch r.next() {
	case '[':
		r.i++
		out, ok = []string{}, true
		valid := r.elements(']', func() bool {
			v, isString, valid := r.member()
			if !valid {
				return false
			}
			out = append(out, v)
			ok = ok && isString
			return true
		})
		if !valid || !r.end() {
			return nil, false
		}
		return out, ok
	case 'n':
		if !r.literal("null") || !r.end() {
			return nil, false
		}
		return nil, true
	}
	if !r.skip(1) || !r.end() {
		return nil, false
	}
	return nil, false
}

// jsonStringMap reads a JSON object of strings the way encoding/json decodes one into a nil
// map[string]string: as [jsonStrings], with a later member for a key winning.
func jsonStringMap(s string) (out map[string]string, ok bool) {
	r := jsonReader{s: s}
	r.space()
	switch r.next() {
	case '{':
		r.i++
		out, ok = map[string]string{}, true
		valid := r.elements('}', func() bool {
			if r.next() != '"' {
				return false
			}
			k, valid := r.str()
			if !valid {
				return false
			}
			r.space()
			if r.next() != ':' {
				return false
			}
			r.i++
			r.space()
			v, isString, valid := r.member()
			if !valid {
				return false
			}
			out[k] = v
			ok = ok && isString
			return true
		})
		if !valid || !r.end() {
			return nil, false
		}
		return out, ok
	case 'n':
		if !r.literal("null") || !r.end() {
			return nil, false
		}
		return nil, true
	}
	if !r.skip(1) || !r.end() {
		return nil, false
	}
	return nil, false
}

// jsonMaxDepth is how deeply encoding/json lets arrays and objects nest.
const jsonMaxDepth = 10000

// jsonReader reads JSON text as encoding/json's scanner does.
type jsonReader struct {
	s string
	i int
}

// next is the byte at the read position, 0 at the end.
func (r *jsonReader) next() byte {
	if r.i < len(r.s) {
		return r.s[r.i]
	}
	return 0
}

// space skips JSON whitespace.
func (r *jsonReader) space() {
	for r.i < len(r.s) && (r.s[r.i] == ' ' || r.s[r.i] == '\t' || r.s[r.i] == '\n' || r.s[r.i] == '\r') {
		r.i++
	}
}

// end reports whether only whitespace is left.
func (r *jsonReader) end() bool {
	r.space()
	return r.i == len(r.s)
}

// elements reads the members of an array or object whose opening bracket was read, through
// closer: each calls member at the member's first byte.
func (r *jsonReader) elements(closer byte, member func() bool) bool {
	r.space()
	if r.next() == closer {
		r.i++
		return true
	}
	for {
		r.space()
		if !member() {
			return false
		}
		r.space()
		switch r.next() {
		case ',':
			r.i++
		case closer:
			r.i++
			return true
		default:
			return false
		}
	}
}

// member reads one array element or object member value bound for a string: its text when it
// is a string, "" otherwise. isString is false for a value that is neither a string nor null.
func (r *jsonReader) member() (v string, isString, valid bool) {
	switch r.next() {
	case '"':
		v, valid = r.str()
		return v, true, valid
	case 'n':
		return "", true, r.literal("null")
	}
	return "", false, r.skip(2)
}

// literal reads word.
func (r *jsonReader) literal(word string) bool {
	if !strings.HasPrefix(r.s[r.i:], word) {
		return false
	}
	r.i += len(word)
	return true
}

// skip reads one value of any type; depth is the nesting level an array or object would open.
func (r *jsonReader) skip(depth int) bool {
	switch c := r.next(); {
	case c == '"':
		_, ok := r.str()
		return ok
	case c == 't':
		return r.literal("true")
	case c == 'f':
		return r.literal("false")
	case c == 'n':
		return r.literal("null")
	case c == '-' || isDigit(c):
		return r.number()
	case c == '[':
		if depth > jsonMaxDepth {
			return false
		}
		r.i++
		return r.elements(']', func() bool { return r.skip(depth + 1) })
	case c == '{':
		if depth > jsonMaxDepth {
			return false
		}
		r.i++
		return r.elements('}', func() bool {
			if r.next() != '"' {
				return false
			}
			if _, ok := r.str(); !ok {
				return false
			}
			r.space()
			if r.next() != ':' {
				return false
			}
			r.i++
			r.space()
			return r.skip(depth + 1)
		})
	}
	return false
}

// number reads a JSON number: -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?.
func (r *jsonReader) number() bool {
	if r.next() == '-' {
		r.i++
	}
	switch c := r.next(); {
	case c == '0':
		r.i++
	case '1' <= c && c <= '9':
		r.i = skipDigits(r.s, r.i)
	default:
		return false
	}
	if r.next() == '.' {
		r.i++
		if !isDigit(r.next()) {
			return false
		}
		r.i = skipDigits(r.s, r.i)
	}
	if c := r.next(); c == 'e' || c == 'E' {
		r.i++
		if c := r.next(); c == '+' || c == '-' {
			r.i++
		}
		if !isDigit(r.next()) {
			return false
		}
		r.i = skipDigits(r.s, r.i)
	}
	return true
}

// str reads a JSON string at its opening quote and decodes it as encoding/json does: an
// invalid UTF-8 byte, or a lone UTF-16 surrogate, becomes U+FFFD.
func (r *jsonReader) str() (string, bool) {
	r.i++ // the opening quote
	var b strings.Builder
	for r.i < len(r.s) {
		c := r.s[r.i]
		switch {
		case c == '"':
			r.i++
			return b.String(), true
		case c < ' ':
			return "", false
		case c == '\\':
			if !r.escape(&b) {
				return "", false
			}
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			r.i++
		default:
			rn, size := utf8.DecodeRuneInString(r.s[r.i:])
			b.WriteRune(rn)
			r.i += size
		}
	}
	return "", false
}

// escape decodes the escape sequence at the read position into b.
func (r *jsonReader) escape(b *strings.Builder) bool {
	if r.i+1 >= len(r.s) {
		return false
	}
	switch e := r.s[r.i+1]; e {
	case '"', '\\', '/':
		b.WriteByte(e)
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'u':
		rn := hex4(r.s, r.i)
		if rn < 0 {
			return false
		}
		r.i += 6
		if utf16.IsSurrogate(rn) {
			if dec := utf16.DecodeRune(rn, hex4(r.s, r.i)); dec != unicode.ReplacementChar {
				r.i += 6
				b.WriteRune(dec)
				return true
			}
			rn = unicode.ReplacementChar
		}
		b.WriteRune(rn)
		return true
	default:
		return false
	}
	r.i += 2
	return true
}

// hex4 reads the \uXXXX escape at s[i:], -1 when there is none.
func hex4(s string, i int) rune {
	if i+6 > len(s) || s[i] != '\\' || s[i+1] != 'u' {
		return -1
	}
	var rn rune
	for _, c := range []byte(s[i+2 : i+6]) {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			c = c - 'A' + 10
		default:
			return -1
		}
		rn = rn*16 + rune(c)
	}
	return rn
}
