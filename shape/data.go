package shape

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// maxDepth bounds how deep toData walks, which also stops a cyclic value.
const maxDepth = 1000

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	numberType        = reflect.TypeFor[json.Number]()
)

// toData reduces v to the data a template runs over: what decoding v's JSON encoding into an
// any would give, numbers as json.Number, except that a struct keeps every field it declares,
// an omitempty field included as its zero value.
func toData(v reflect.Value, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("the value is nested too deeply, or refers to itself")
	}
	if !v.IsValid() {
		return nil, nil //nolint:nilnil // JSON null
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil, nil //nolint:nilnil // JSON null
	}
	if v.Type() == numberType {
		return json.Number(v.String()), nil
	}
	if marshals(v) {
		return viaJSON(v)
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return toData(v.Elem(), depth+1)
	case reflect.Struct:
		return structData(v, depth)
	case reflect.Map:
		return mapData(v, depth)
	case reflect.Slice:
		if v.IsNil() {
			return nil, nil //nolint:nilnil // JSON null
		}
		if v.Type().Elem().Kind() == reflect.Uint8 && !marshalsType(v.Type().Elem()) {
			return base64.StdEncoding.EncodeToString(v.Bytes()), nil
		}
		return listData(v, depth)
	case reflect.Array:
		return listData(v, depth)
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Number(strconv.FormatInt(v.Int(), 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return json.Number(strconv.FormatUint(v.Uint(), 10)), nil
	case reflect.Float32, reflect.Float64:
		return floatNumber(v.Float(), v.Type().Bits())
	default:
		return nil, fmt.Errorf("a %s has no JSON form", v.Type())
	}
}

// marshals reports whether v encodes itself, as encoding/json would ask it to.
func marshals(v reflect.Value) bool {
	if !v.CanInterface() {
		return false
	}
	if marshalsType(v.Type()) {
		return true
	}
	return v.Kind() != reflect.Pointer && v.CanAddr() && marshalsType(reflect.PointerTo(v.Type()))
}

func marshalsType(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType)
}

// viaJSON is v's own JSON encoding, decoded.
func viaJSON(v reflect.Value) (any, error) {
	if v.Kind() != reflect.Pointer && v.CanAddr() {
		v = v.Addr()
	}
	b, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, err //nolint:wrapcheck // Execute adds the package prefix
	}
	return decode(b)
}

func decode(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err //nolint:wrapcheck // Execute adds the package prefix
	}
	return out, nil
}

func listData(v reflect.Value, depth int) (any, error) {
	out := make([]any, v.Len())
	for i := range out {
		item, err := toData(v.Index(i), depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = item
	}
	return out, nil
}

func mapData(v reflect.Value, depth int) (any, error) {
	if v.IsNil() {
		return nil, nil //nolint:nilnil // JSON null
	}
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		key, err := mapKey(iter.Key())
		if err != nil {
			return nil, err
		}
		item, err := toData(iter.Value(), depth+1)
		if err != nil {
			return nil, err
		}
		out[key] = item
	}
	return out, nil
}

// mapKey is a map key as encoding/json writes it.
func mapKey(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if tm, ok := textMarshaler(k); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		b, err := tm.MarshalText()
		return string(b), err
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	default:
		return "", fmt.Errorf("a map keyed by %s has no JSON form", k.Type())
	}
}

func structData(v reflect.Value, depth int) (any, error) {
	out := map[string]any{}
	for _, f := range jsonFields(v.Type()) {
		fv, err := v.FieldByIndexErr(f.index)
		if err != nil { // through a nil embedded pointer
			out[f.name] = nil
			continue
		}
		var item any
		if f.quoted {
			item, err = quoted(fv)
		} else {
			item, err = toData(fv, depth+1)
		}
		if err != nil {
			return nil, err
		}
		out[f.name] = item
	}
	return out, nil
}

// quoted is a field tagged `json:",string"`: its JSON encoding, as a string.
func quoted(v reflect.Value) (any, error) {
	if !v.CanInterface() {
		return toData(v, 0)
	}
	b, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, err //nolint:wrapcheck // Execute adds the package prefix
	}
	return string(b), nil
}

// field is one property of a struct's JSON encoding.
type field struct {
	name   string
	index  []int
	tagged bool
	quoted bool
}

// jsonFields lists the properties encoding/json writes for struct type t, by the same rules:
// exported fields, renamed or skipped by their json tags, with an untagged embedded struct's
// fields promoted and a name conflict settled by depth, then by a tag.
func jsonFields(t reflect.Type) []field {
	var all []field
	collectFields(t, nil, map[reflect.Type]bool{}, &all)

	byName := map[string][]field{}
	var order []string
	for _, f := range all {
		if _, seen := byName[f.name]; !seen {
			order = append(order, f.name)
		}
		byName[f.name] = append(byName[f.name], f)
	}
	var out []field
	for _, name := range order {
		if f, ok := dominant(byName[name]); ok {
			out = append(out, f)
		}
	}
	return out
}

func collectFields(t reflect.Type, index []int, visiting map[reflect.Type]bool, out *[]field) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)
	for i := range t.NumField() {
		sf := t.Field(i)
		ft := sf.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if sf.Anonymous {
			if !sf.IsExported() && ft.Kind() != reflect.Struct {
				continue
			}
		} else if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		at := append(append([]int(nil), index...), i)
		if name == "" && sf.Anonymous && ft.Kind() == reflect.Struct {
			collectFields(ft, at, visiting, out)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		f := field{name: sf.Name, index: at, tagged: name != ""}
		if name != "" {
			f.name = name
		}
		if hasOption(opts, "string") {
			switch ft.Kind() {
			case reflect.Bool, reflect.String,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
				reflect.Float32, reflect.Float64:
				f.quoted = true
			default:
			}
		}
		*out = append(*out, f)
	}
}

func hasOption(opts, want string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == want {
			return true
		}
	}
	return false
}

// dominant picks the field encoding/json writes among those sharing a name: the shallowest,
// and among the shallowest the one tagged; none when that leaves more than one.
func dominant(fs []field) (field, bool) {
	shallowest := len(fs[0].index)
	for _, f := range fs[1:] {
		shallowest = min(shallowest, len(f.index))
	}
	var top []field
	for _, f := range fs {
		if len(f.index) == shallowest {
			top = append(top, f)
		}
	}
	if len(top) == 1 {
		return top[0], true
	}
	var tagged []field
	for _, f := range top {
		if f.tagged {
			tagged = append(tagged, f)
		}
	}
	if len(tagged) == 1 {
		return tagged[0], true
	}
	return field{}, false
}

// floatNumber formats f as encoding/json does.
func floatNumber(f float64, bits int) (any, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("the number %s has no JSON form", strconv.FormatFloat(f, 'g', -1, bits))
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			format = 'e'
		}
	}
	b := strconv.AppendFloat(nil, f, format, -1, bits)
	if format == 'e' {
		// e-09 becomes e-9.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return json.Number(b), nil
}

func textMarshaler(k reflect.Value) (encoding.TextMarshaler, bool) {
	if !k.CanInterface() {
		return nil, false
	}
	return reflect.TypeAssert[encoding.TextMarshaler](k)
}
