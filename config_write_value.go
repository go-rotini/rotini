package rotini

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/go-rotini/rotini/internal/cfgedit"
)

// configValue checks raw against every input that reads the key (a value must satisfy each of
// them) and returns what to write: numbers and booleans as such, an enum value in its declared
// spelling, anything else as the string given; a list as a list and a map as a map, except in
// a dotenv file, where every value is one string.
func configValue(rtx *Context, keys []ConfigKey, raw string, format cfgedit.Format, parse func(string) error) (any, error) {
	view := rtx.osView()
	var value any
	for i, k := range keys {
		v, err := checkConfigKey(k, raw, view, parse)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			value = v.written(format)
		}
	}
	return value, nil
}

// checkedValue is a value that passed a key's checks: its items as text, in the enum's
// declared spelling, and as the key's type parsed them.
type checkedValue struct {
	key   ConfigKey
	texts []string
	typed reflect.Value // invalid for a key of a custom type
}

// checkConfigKey checks raw against one key's declaration, the way the command line checks a
// flag's value.
func checkConfigKey(k ConfigKey, raw string, view *osView, parse func(string) error) (checkedValue, error) {
	label := "config key " + k.Key
	if k.Object {
		return checkedValue{}, usageBind(channelConfig, k.Key, label+" holds an object; edit the configuration file by hand", nil)
	}
	fd := k.flagDef()
	texts := []string{raw}
	if isArrayType(k.Type) || isMapType(k.Type) {
		var err error
		if texts, err = splitValue(raw, k.separator()); err != nil {
			return checkedValue{}, usageBind(channelConfig, k.Key, fmt.Sprintf("%s: %v", label, err), err)
		}
	}
	texts = flagEnum(fd).canonical(texts)
	if err := checkFlagValues(fd, label, texts, view.base(), view.clockRef()); err != nil {
		return checkedValue{}, configValueError(k.Key, err)
	}
	out := checkedValue{key: k, texts: texts}
	if t, ok := typeForName(k.Type); ok {
		out.typed = reflect.New(t).Elem()
		if err := coerceFlagValues(out.typed, fd, texts, view.clockRef()); err != nil {
			return checkedValue{}, configValueError(k.Key, coerceFailure(label, "", err, k.Secret))
		}
	} else if parse == nil {
		return checkedValue{}, internalBind(channelConfig, k.Key, fmt.Sprintf("%s has a custom type (%s); pass rotini.ParseConfigValue to check its value", label, k.Type), nil)
	}
	if parse != nil {
		if err := parse(raw); err != nil {
			return checkedValue{}, usageBind(channelConfig, k.Key, fmt.Sprintf("%s: %s", label, err), err)
		}
	}
	return out, nil
}

// configValueError turns a value check's error into the usage (or, for the author's mistake,
// internal) [*InputError] a config write reports, keeping its message and suggestion facts.
func configValueError(key string, err error) error {
	pe, ok := errors.AsType[*ParseError](err)
	if !ok {
		return usageBind(channelConfig, key, err.Error(), err)
	}
	if pe.Kind == ParseKindInternal {
		return internalBind(channelConfig, key, pe.Msg, err)
	}
	e := usageBind(channelConfig, key, pe.Msg, err)
	e.Token, e.Candidates = pe.Token, pe.Candidates
	return e
}

// flagDef is the key's declaration in the form the command line's checks read.
func (k ConfigKey) flagDef() FlagDef {
	return FlagDef{
		Name: k.Key, Type: k.Type, Enum: k.Enum, EnumValues: k.EnumValues, IgnoreCase: k.IgnoreCase,
		Layout: k.Layout, Layouts: k.Layouts, Separator: k.separator(), Secret: k.Secret, Constraints: k.Constraints,
	}
}

// separator is what splits a list or map value given as one string.
func (k ConfigKey) separator() string {
	if k.Separator == "" {
		return ","
	}
	return k.Separator
}

// written is the value as it goes into the file.
func (v checkedValue) written(format cfgedit.Format) any {
	k := v.key
	list, isMap := isArrayType(k.Type), isMapType(k.Type)
	if format == cfgedit.Dotenv {
		if !list && !isMap {
			return v.texts[0]
		}
		return strings.Join(v.texts, k.separator())
	}
	native := nativeType(elemTypeName(k.Type)) && v.typed.IsValid()
	switch {
	case list:
		out := make([]any, len(v.texts))
		for i, s := range v.texts {
			out[i] = s
			if native && i < v.typed.Len() {
				out[i] = nativeValue(v.typed.Index(i))
			}
		}
		return out
	case isMap:
		out := make(map[string]any, len(v.texts))
		for _, pair := range v.texts {
			key, val, _ := strings.Cut(pair, "=")
			out[key] = val
			if native && v.typed.Type().Key().Kind() == reflect.String {
				if e := v.typed.MapIndex(reflect.ValueOf(key).Convert(v.typed.Type().Key())); e.IsValid() {
					out[key] = nativeValue(e)
				}
			}
		}
		return out
	case native:
		return nativeValue(v.typed)
	}
	return v.texts[0]
}

// elemTypeName is a list's or map's element type, else typ itself, without a pointer.
func elemTypeName(typ string) string {
	typ = strings.TrimPrefix(typ, "[]")
	if rest, ok := strings.CutPrefix(typ, "map["); ok {
		if _, val, ok := strings.Cut(rest, "]"); ok {
			typ = val
		}
	}
	return strings.TrimPrefix(typ, "*")
}

// nativeType reports whether a type's values are written as numbers or booleans.
func nativeType(typ string) bool {
	return typ == "bool" || typ == "count" || isNumericType(typ)
}

// nativeValue is a parsed number or boolean as cfgedit writes it.
func nativeValue(v reflect.Value) any {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	}
	return v.Interface()
}

// typeForName is the Go type a generated input of the named type reads into, for the types
// rotini parses itself: Go's numbers, bool and string, the path kinds, rotini's value types
// and lists, maps and pointers of them. It reports false for anything else, such as a type a
// spec takes from `import:`.
func typeForName(name string) (reflect.Type, bool) {
	if elem, ok := strings.CutPrefix(name, "[]"); ok {
		t, ok := typeForName(elem)
		if !ok {
			return nil, false
		}
		return reflect.SliceOf(t), true
	}
	if rest, ok := strings.CutPrefix(name, "map["); ok {
		key, val, ok := strings.Cut(rest, "]")
		if !ok {
			return nil, false
		}
		kt, kok := typeForName(key)
		vt, vok := typeForName(val)
		if !kok || !vok || val == "any" || !kt.Comparable() {
			return nil, false
		}
		return reflect.MapOf(kt, vt), true
	}
	if t, ok := scalarTypeFor(strings.TrimPrefix(name, "rotini.")); ok {
		return t, true
	}
	if elem, ok := strings.CutPrefix(name, "*"); ok { // a nullable scalar
		if t, ok := typeForName(elem); ok {
			return reflect.PointerTo(t), true
		}
	}
	return nil, false
}

// scalarTypeFor is the Go type of a scalar type name rotini parses itself.
func scalarTypeFor(name string) (reflect.Type, bool) {
	str := reflect.TypeFor[string]()
	types := map[string]reflect.Type{
		"string": str, "existingfile": str, "existingdir": str, "inputfile": str, "outputfile": str,
		"bool": reflect.TypeFor[bool](), "int": reflect.TypeFor[int](), "count": reflect.TypeFor[int](),
		"int8": reflect.TypeFor[int8](), "int16": reflect.TypeFor[int16](), "int32": reflect.TypeFor[int32](),
		"int64": reflect.TypeFor[int64](), "uint": reflect.TypeFor[uint](), "uint8": reflect.TypeFor[uint8](),
		"uint16": reflect.TypeFor[uint16](), "uint32": reflect.TypeFor[uint32](), "uint64": reflect.TypeFor[uint64](),
		"float32": reflect.TypeFor[float32](), "float64": reflect.TypeFor[float64](),
		"time.Duration": reflect.TypeFor[time.Duration](), "time.Time": reflect.TypeFor[time.Time](),
		"*time.Location": reflect.TypeFor[*time.Location](), "*url.URL": reflect.TypeFor[*url.URL](),
		"mail.Address": reflect.TypeFor[mail.Address](), "net.HardwareAddr": reflect.TypeFor[net.HardwareAddr](),
		"netip.Addr": reflect.TypeFor[netip.Addr](), "netip.Prefix": reflect.TypeFor[netip.Prefix](),
		"netip.AddrPort": reflect.TypeFor[netip.AddrPort](), "*regexp.Regexp": reflect.TypeFor[*regexp.Regexp](),
		"ByteSize": reflect.TypeFor[ByteSize](), "HexBytes": reflect.TypeFor[HexBytes](),
		"Base64Bytes": reflect.TypeFor[Base64Bytes](), "Glob": reflect.TypeFor[Glob](),
	}
	t, ok := types[name]
	return t, ok
}
