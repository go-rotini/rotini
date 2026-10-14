package cfgedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/yaml"
)

// verify decodes after and checks that it equals want, the original document's values with
// the intended change applied. A failure means the splice changed something it shouldn't
// have, or missed what it should have changed; nothing is returned then. An unchanged file is
// checked too: it's right only when it already held the wanted values.
func verify(format Format, after []byte, want map[string]any) ([]byte, error) {
	got, err := decode(format, after)
	if err != nil {
		return nil, fmt.Errorf("%w: the edited file doesn't decode: %w", ErrVerify, err)
	}
	if body := after[bomLen(after):]; format == JSON && len(bytes.TrimSpace(body)) > 0 && !json.Valid(body) {
		return nil, fmt.Errorf("%w: the edited file isn't valid JSON", ErrVerify)
	}
	if !equalValue(want, got) {
		return nil, fmt.Errorf("%w: the edited file decodes to different values", ErrVerify)
	}
	return after, nil
}

// decode reads src with the codec rotini's config reader uses for format, past a byte-order
// mark. A document with no content (empty, or only comments) is an empty mapping.
func decode(format Format, src []byte) (map[string]any, error) {
	src = src[bomLen(src):]
	var codec recon.Codec
	switch format {
	case YAML:
		codec = recon.YAML
		if yamlBlank(src) {
			return decodeBlank(format, codec, src)
		}
	case JSON, JSONC:
		codec = recon.JSON
		if format == JSONC {
			codec = recon.JSONC
		}
		if jsonBlank(src) {
			return decodeBlank(format, codec, src)
		}
	case TOML:
		codec = recon.TOML
	case Dotenv:
		codec = recon.Dotenv
	default:
		return nil, fmt.Errorf("cfgedit: format %q: %w", format, ErrUnsupported)
	}
	m, err := codec.Decode(src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", format, err)
	}
	return m, nil
}

// decodeBlank checks a document with no value, only comments, by decoding it with an empty
// mapping after it, and returns that empty mapping.
func decodeBlank(format Format, codec recon.Codec, src []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return map[string]any{}, nil
	}
	if _, err := codec.Decode(append(slices.Clip(src), "\n{}"...)); err != nil {
		return nil, fmt.Errorf("parse %s: %w", format, err)
	}
	return map[string]any{}, nil
}

func yamlBlank(src []byte) bool {
	f, err := yaml.Parse(src)
	if err != nil {
		return false
	}
	return len(f.Docs) == 0 || (len(f.Docs) == 1 && len(f.Docs[0].Children) == 0)
}

// setPath sets path to v in m, creating mappings on the way.
func setPath(m map[string]any, path []string, v any) {
	for _, seg := range path[:len(path)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[seg] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// unsetPath deletes path from m, then deletes each mapping on the path the deletion leaves
// empty, deepest first, except the first keep. An absent key changes nothing.
func unsetPath(m map[string]any, path []string, keep int) {
	chain := []map[string]any{m}
	for _, seg := range path[:len(path)-1] {
		next, ok := chain[len(chain)-1][seg].(map[string]any)
		if !ok {
			return
		}
		chain = append(chain, next)
	}
	if _, ok := chain[len(chain)-1][path[len(path)-1]]; !ok {
		return // an absent key leaves the document as it is
	}
	delete(chain[len(chain)-1], path[len(path)-1])
	for i := len(chain) - 1; i > 0 && i > keep && len(chain[i]) == 0; i-- {
		delete(chain[i-1], path[i-1])
	}
}

// equalValue compares two decoded trees, treating numbers by value whatever their Go type.
func equalValue(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !equalValue(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalValue(x[i], y[i]) {
				return false
			}
		}
		return true
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	return reflect.DeepEqual(a, b)
}

// number returns v as a canonical decimal string when it is a number, so 5, int64(5),
// uint64(5) and 5.0 all compare equal.
func number(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f == math.Trunc(f) && math.Abs(f) < 1<<63 {
			return strconv.FormatInt(int64(f), 10), true
		}
		return strconv.FormatFloat(f, 'g', -1, 64), true
	}
	return "", false
}

// readsIntegersExactly reports whether the reader rotini uses for format, JSON, JSONC or YAML,
// reads back every 64-bit integer exactly, beyond ±2^53 and above the largest int64.
func readsIntegersExactly(format Format) bool {
	switch format {
	case JSON:
		return jsonExact()
	case JSONC:
		return jsoncExact()
	case YAML:
		return yamlExact()
	}
	return false
}

// lazy holds a value computed on first use, as [sync.OnceValue] does. Its function is passed
// at the call site rather than stored in a package variable, so a program that never asks for
// the value links neither the function nor what it calls: the probes below would otherwise link
// every format's reader, and the YAML 1.1 pattern the regexp engine.
type lazy[T any] struct {
	once sync.Once
	get  func() T
}

func (l *lazy[T]) value(f func() T) T {
	l.once.Do(func() { l.get = sync.OnceValue(f) })
	return l.get()
}

var jsonExactOnce, jsoncExactOnce, yamlExactOnce, tomlReadsNULOnce lazy[bool]

func jsonExact() bool {
	return jsonExactOnce.value(func() bool {
		return readsBack(JSON, `{"a": 18446744073709551615, "b": -9223372036854775808, "c": 9007199254740993}`)
	})
}

func jsoncExact() bool {
	return jsoncExactOnce.value(func() bool {
		return readsBack(JSONC, `{"a": 18446744073709551615, "b": -9223372036854775808, "c": 9007199254740993}`)
	})
}

func yamlExact() bool {
	return yamlExactOnce.value(func() bool {
		return readsBack(YAML, "a: 18446744073709551615\nb: -9223372036854775808\nc: 9007199254740993\n")
	})
}

// readsBack reports whether src, a document with the keys a, b and c, decodes to the
// integers its text holds.
func readsBack(format Format, src string) bool {
	m, err := decode(format, []byte(src))
	if err != nil {
		return false
	}
	for k, want := range map[string]string{"a": "18446744073709551615", "b": "-9223372036854775808", "c": "9007199254740993"} {
		if got, ok := number(m[k]); !ok || got != want {
			return false
		}
	}
	return true
}

// tomlReadsNUL reports whether the TOML reader rotini uses reads back a key holding U+0000 as
// one key, apart from the dotted path its pieces would make.
func tomlReadsNUL() bool {
	return tomlReadsNULOnce.value(func() bool {
		m, err := decode(TOML, []byte(`"a\u0000b" = 1`+"\n[a]\nb = 2\n"))
		if err != nil {
			return false
		}
		nul, _ := number(m["a\x00b"])
		sub, _ := m["a"].(map[string]any)
		dotted, _ := number(sub["b"])
		return nul == "1" && dotted == "2" && len(m) == 2
	})
}
