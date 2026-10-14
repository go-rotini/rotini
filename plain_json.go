package rotini

import (
	"encoding/base64"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-rotini/recon"
)

// errPlainJSON is the failure appendPlainJSON reports where encoding/json would fail (a NaN
// or infinite float, a time outside years 0–9999, a malformed number, too deep a nesting);
// callers only test it for nil.
var errPlainJSON error = plainJSONError{}

type plainJSONError struct{}

func (plainJSONError) Error() string { return "value has no JSON form" }

// maxPlainJSONDepth is encoding/json's nesting limit.
const maxPlainJSONDepth = 10000

// marshalPlain is json.Marshal(plainValue(v)), byte for byte, without linking encoding/json.
// A reconciled value holds only the types recon builds (null, string, int64, uint64, float64,
// bool, time.Time, time.Duration, json.Number text, [recon.RawValue], lists and string-keyed
// maps of those), so the encoder covers exactly those.
func marshalPlain(v recon.Value) ([]byte, error) {
	return appendPlainJSON(nil, v, 0)
}

func appendPlainJSON(b []byte, v recon.Value, depth int) ([]byte, error) {
	switch v.Kind() {
	case recon.MapKind:
		depth++
		m, err := v.AsMap()
		if err != nil || depth > maxPlainJSONDepth {
			return nil, errPlainJSON
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, k)
			b = append(b, ':')
			if b, err = appendPlainJSON(b, m[k], depth); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case recon.SliceKind:
		depth++
		items, err := v.AsSlice()
		if err != nil || depth > maxPlainJSONDepth {
			return nil, errPlainJSON
		}
		b = append(b, '[')
		for i, it := range items {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendPlainJSON(b, it, depth); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	}
	return appendPlainScalar(b, v.Any())
}

func appendPlainScalar(b []byte, x any) ([]byte, error) {
	switch x := x.(type) {
	case nil:
		return append(b, "null"...), nil
	case string:
		return appendJSONString(b, x), nil
	case bool:
		return strconv.AppendBool(b, x), nil
	case int64:
		return strconv.AppendInt(b, x, 10), nil
	case uint64:
		return strconv.AppendUint(b, x, 10), nil
	case time.Duration:
		return strconv.AppendInt(b, int64(x), 10), nil
	case float64:
		return appendJSONFloat(b, x)
	case time.Time:
		raw, err := x.MarshalJSON()
		if err != nil {
			return nil, errPlainJSON
		}
		return append(b, raw...), nil
	case recon.RawValue:
		b = append(b, `{"Format":`...)
		b = appendJSONString(b, x.Format)
		b = append(b, `,"Data":`...)
		if x.Data == nil {
			b = append(b, "null"...)
		} else {
			b = append(b, '"')
			b = base64.StdEncoding.AppendEncode(b, x.Data)
			b = append(b, '"')
		}
		return append(b, '}'), nil
	}
	// json.Number, named by its kind so this file needs no encoding/json import.
	if s, ok := numberText(x); ok {
		if s == "" {
			return append(b, '0'), nil
		}
		if !validJSONNumber(s) {
			return nil, errPlainJSON
		}
		return append(b, s...), nil
	}
	return nil, errPlainJSON
}

// numberText reads a json.Number (a named string type) without importing encoding/json.
func numberText(x any) (string, bool) {
	type stringer interface {
		String() string
		Int64() (int64, error)
		Float64() (float64, error)
	}
	n, ok := x.(stringer)
	if !ok {
		return "", false
	}
	return n.String(), true
}

// appendJSONFloat formats f as encoding/json does: the shortest representation, in exponent
// form only below 1e-6 or from 1e21 up, with a one-digit negative exponent unpadded.
func appendJSONFloat(b []byte, f float64) ([]byte, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, errPlainJSON
	}
	abs, format := math.Abs(f), byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		// clean up e-09 to e-9
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b, nil
}

const hexDigits = "0123456789abcdef"

// appendJSONString quotes s as encoding/json does: HTML-safe (<, >, & escaped), U+2028 and
// U+2029 escaped, invalid UTF-8 replaced with U+FFFD.
func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"', '\\':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, `�`...)
			i += size
			start = i
			continue
		}
		if r == ' ' || r == ' ' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hexDigits[r&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

// validJSONNumber reports whether s is a JSON number literal.
func validJSONNumber(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		if s = s[1:]; s == "" {
			return false
		}
	}
	switch {
	case s[0] == '0':
		s = s[1:]
	case '1' <= s[0] && s[0] <= '9':
		s = s[skipDigits(s, 1):]
	default:
		return false
	}
	if len(s) >= 2 && s[0] == '.' && '0' <= s[1] && s[1] <= '9' {
		s = s[skipDigits(s, 2):]
	}
	if len(s) >= 2 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s[0] == '+' || s[0] == '-' {
			if s = s[1:]; s == "" {
				return false
			}
		}
		s = s[skipDigits(s, 0):]
	}
	return s == ""
}
