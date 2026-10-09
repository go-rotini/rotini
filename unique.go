package rotini

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// checkUniqueItems enforces [Constraints.UniqueItems] on an input's command-line values: no two
// may read as the same value. key canonicalizes one value, so `01` and `1` collide as ints.
func checkUniqueItems(label string, vals []string, key func(string) string, secret bool) error {
	if len(vals) < 2 {
		return nil
	}
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		k := key(v)
		if seen[k] {
			return duplicateItem(label, v, secret)
		}
		seen[k] = true
	}
	return nil
}

// duplicateItem is the error for a value a uniqueItems input holds twice.
func duplicateItem(label, v string, secret bool) error {
	return constraintViolation("%s must not repeat a value (got %q twice)", label, redactValue(v, secret))
}

// uniqueKeyFor returns how a command-line value of type typ compares for uniqueness: as the
// value its element type reads it as, or as written when it doesn't read (binding reports
// that). layout is a time input's layout; enum and ignoreCase fold a case-insensitive enum
// value to its declared spelling.
func uniqueKeyFor(typ, layout string, enum []string, ignoreCase bool) func(string) string {
	elem := constraintElemType(typ)
	if ignoreCase && len(enum) > 0 {
		return func(v string) string { return canonicalEnum(enum, []string{v})[0] }
	}
	if t, ok := shapeTypes[elem]; ok {
		return func(v string) string {
			f := reflect.New(t).Elem()
			if coerce(f, []string{v}) != nil {
				return v
			}
			return fmt.Sprint(f.Interface())
		}
	}
	parse := uniqueParsers[elem]
	if elem == "time.Time" {
		parse = func(v string) (string, error) {
			var (
				t   time.Time
				err error
			)
			if layout == "" {
				t, err = time.Parse(time.RFC3339, strings.TrimSpace(v))
			} else {
				t, err = parseTimeLayout(v, layout)
			}
			return t.UTC().Format(time.RFC3339Nano), err
		}
	}
	if parse == nil {
		return func(v string) string { return v }
	}
	return func(v string) string {
		if k, err := parse(v); err == nil {
			return k
		}
		return v
	}
}

// uniqueParsers canonicalize the values of the types whose spellings vary beyond what
// shapeTypes covers, by definition type name.
var uniqueParsers = map[string]func(string) (string, error){
	"rotini.ByteSize": func(v string) (string, error) {
		n, err := parseByteSize(v)
		return strconv.FormatInt(n, 10), err
	},
	"netip.Addr": func(v string) (string, error) {
		a, err := netip.ParseAddr(v)
		return a.String(), err
	},
	"netip.Prefix": func(v string) (string, error) {
		p, err := netip.ParsePrefix(v)
		return p.String(), err
	},
	"netip.AddrPort": func(v string) (string, error) {
		p, err := netip.ParseAddrPort(v)
		return p.String(), err
	},
	"net.HardwareAddr": func(v string) (string, error) {
		m, err := net.ParseMAC(v)
		return m.String(), err
	},
}

// duplicateDocument enforces UniqueItems on a list of objects: two elements are the same when
// their documents are, compared as canonical JSON (object keys sorted). It returns the first
// repeated document, or "" when none repeats.
func duplicateDocument(docs []map[string]any) string {
	seen := make(map[string]bool, len(docs))
	for _, d := range docs {
		b, err := json.Marshal(d)
		if err != nil {
			continue
		}
		if seen[string(b)] {
			return string(b)
		}
		seen[string(b)] = true
	}
	return ""
}

// checkUniqueTyped enforces UniqueItems on a bound list's elements: they compare as values, a
// time by its instant, a URL or a text-marshaling type by its text, a byte slice by its bytes,
// and a struct, map or slice as canonical JSON.
func checkUniqueTyped(label string, elems []reflect.Value, secret bool) error {
	seen := make(map[string]bool, len(elems))
	for _, e := range elems {
		k, shown := typedUniqueKey(e)
		if seen[k] {
			return duplicateItem(label, shown, secret)
		}
		seen[k] = true
	}
	return nil
}

// typedUniqueKey is how one bound element compares for uniqueness, and how an error shows it.
func typedUniqueKey(v reflect.Value) (key, shown string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "null", "null"
		}
		if u, ok := reflect.TypeAssert[*url.URL](v); ok {
			return u.String(), u.String()
		}
		v = v.Elem()
	}
	if t, ok := reflect.TypeAssert[time.Time](v); ok {
		s := t.UTC().Format(time.RFC3339Nano)
		return s, s
	}
	if v.CanInterface() {
		if m, ok := reflect.TypeAssert[encoding.TextMarshaler](v); ok {
			if b, err := m.MarshalText(); err == nil {
				return string(b), string(b)
			}
		}
	}
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes()), fmt.Sprint(v.Interface())
		}
		fallthrough
	case reflect.Struct, reflect.Map, reflect.Array:
		if b, err := json.Marshal(v.Interface()); err == nil {
			return string(b), string(b)
		}
	}
	s := fmt.Sprint(v.Interface())
	return v.Type().String() + ":" + s, s
}

// duplicateObjectError is a list of objects holding the same document twice; the binder names
// the input.
type duplicateObjectError string

func (e duplicateObjectError) Error() string {
	return fmt.Sprintf("must not repeat a value (got %s twice)", string(e))
}

// checkRepeat enforces [FlagDef.NoRepeat]: a flag that takes one value, given more than once
// on the command line, is an error naming the first and last values. onArgv reports whether
// the command line set the flag; a fallback value is never a repeat.
func checkRepeat(fd FlagDef, label string, vals []string, onArgv bool) error {
	if !fd.NoRepeat || !onArgv || len(vals) < 2 {
		return nil
	}
	return &ParseError{
		Kind: ParseKindConstraintViolation,
		Msg: fmt.Sprintf("%s was given more than once (%q, then %q); it takes one value",
			label, redactValue(vals[0], fd.Secret), redactValue(vals[len(vals)-1], fd.Secret)),
		Flag: label,
	}
}
