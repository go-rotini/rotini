package rotini

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/go-rotini/recon"
)

// A configuration file can hold a list or a map where an input expects one value. Binding
// either would fill the input with an arbitrary item or with Go's printout of the value, so
// it is a usage error naming the input and where the value came from.

// holdsOneValue reports whether a field of type t holds a single value: anything but a list,
// a map, a struct or an interface, plus the types that parse their own text, such as net.IP
// and time.Time.
func holdsOneValue(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if _, _, ok := extraKindOf(t); ok {
		return true
	}
	if t.Implements(textUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	if d := derefType(t); d != t {
		return holdsOneValue(d)
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct, reflect.Interface:
		return false
	}
	return true
}

// shapeKind names a list or map value for a message, "" for anything else.
func shapeKind(v recon.Value) string {
	switch v.Kind() {
	case recon.SliceKind:
		return "a list"
	case recon.MapKind:
		return "a map"
	}
	return ""
}

// configShapeProblem says why a configuration value can't fill a config input of type t, or ""
// when it can: a list or map for an input that holds one value, or a list or map inside a list
// or map whose items hold one value each.
func configShapeProblem(val recon.Value, t reflect.Type) string {
	kind := shapeKind(val)
	if kind == "" {
		return ""
	}
	if holdsOneValue(t) {
		return "takes one value, not " + kind
	}
	switch t = derefType(t); t.Kind() {
	case reflect.Slice:
		if holdsOneValue(t.Elem()) && val.Kind() == recon.SliceKind {
			return itemProblem(val)
		}
	case reflect.Map:
		if holdsOneValue(t.Elem()) && val.Kind() == recon.MapKind {
			return entryProblem(val, "", true)
		}
	}
	return ""
}

// flagShapeProblem says why a configuration value can't fill a flag or argument of type t that
// the command line fills one occurrence at a time, or "" when it can. A map is read as its
// key=value entries, nested maps as dotted keys, so only a list inside it is a problem; an
// object flag holds a map, and a list of objects one per item.
func flagShapeProblem(val recon.Value, t reflect.Type, object bool) string {
	kind := shapeKind(val)
	switch {
	case kind == "":
		return ""
	case object:
		if val.Kind() == recon.SliceKind && derefType(t).Kind() != reflect.Slice {
			return "takes one object, not a list"
		}
		return ""
	case holdsOneValue(t):
		return "takes one value, not " + kind
	case val.Kind() == recon.SliceKind:
		return itemProblem(val)
	}
	return entryProblem(val, "", false)
}

// objectBlock is an object flag's configuration block as the flag's occurrences: its key=value
// entries, quoted, or one JSON document when the block holds a list, which entries can't spell.
func objectBlock(val recon.Value, entries []string) []string {
	if entryProblem(val, "", false) != "" {
		if raw, err := marshalPlain(val); err == nil {
			return []string{string(raw)}
		}
	}
	quoteObjectPairs(entries)
	return entries
}

// itemProblem names the first item of a list that is itself a list or a map.
func itemProblem(val recon.Value) string {
	items, err := val.AsSlice()
	if err != nil {
		return ""
	}
	for i, it := range items {
		if kind := shapeKind(it); kind != "" {
			return fmt.Sprintf("takes one value per item, not %s (item %d)", kind, i+1)
		}
	}
	return ""
}

// entryProblem names the first entry of a map, in key order and below prefix, that is a list,
// or with maps also a map.
func entryProblem(val recon.Value, prefix string, maps bool) string {
	m, err := val.AsMap()
	if err != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, key := m[k], prefix+k
		switch {
		case v.Kind() == recon.SliceKind:
			return fmt.Sprintf("takes one value per key, not a list (key %s)", key)
		case v.Kind() == recon.MapKind && maps:
			return fmt.Sprintf("takes one value per key, not a map (key %s)", key)
		case v.Kind() == recon.MapKind:
			if p := entryProblem(v, key+".", false); p != "" {
				return p
			}
		}
	}
	return ""
}
