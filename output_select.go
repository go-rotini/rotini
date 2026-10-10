package rotini

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Selection is a value reduced to some of its fields by [SelectFields]. [Context.WriteOutput]
// and [Context.WriteOutputItem] accept it in place of the type it was selected from, and check
// it against the declared schema with every `required` removed, since a selection leaves fields
// out. It writes as json, yaml or toml; a renderer receives it as is, and can marshal it to
// JSON. Its zero value is not a selection, and writing one is an internal error.
type Selection struct {
	from reflect.Type // the selected value's type, pointers removed
	data any          // JSON-shaped: map[string]any, []any, json.Number, string, bool, nil
}

// MarshalJSON writes the selected fields.
func (s Selection) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.data) //nolint:wrapcheck // the encoder names the value
}

// MarshalYAML hands the selected fields to a yaml encoder.
func (s Selection) MarshalYAML() (any, error) { return nativeNumbers(s.data), nil }

// MarshalTOML hands the selected fields to a toml encoder.
func (s Selection) MarshalTOML() (any, error) { return nativeNumbers(s.data), nil }

// SelectFields returns v keeping only fields, named by their JSON property names, the names
// `-o json` shows. A field with dots reaches into nested objects: owner.login keeps the login
// of each item's owner. at is the dotted path to the items the fields belong to: the spec's
// `values_from` without its "output" prefix, so "" for v itself and "tasks" for v's tasks.
// Lists on the way, v itself included, are stepped through, so each element is reduced, and
// everything outside at (an envelope's total) is kept. A field an item lacks is left out of
// that item.
//
// Sort before selecting, since the sort field need not be selected:
//
//	if in.Flags.SortBy != "" {
//		if err := rotini.SortBy(list.Tasks, in.Flags.SortBy, in.Flags.Reverse); err != nil {
//			return err
//		}
//	}
//	if len(in.Flags.JSON) > 0 {
//		sel, err := rotini.SelectFields(list, "tasks", in.Flags.JSON)
//		if err != nil {
//			return err
//		}
//		return rtx.WriteOutput(sel, "json", nil)
//	}
//
// v is converted through JSON, which is fine for the lists a command line prints.
func SelectFields(v any, at string, fields []string) (Selection, error) {
	from, data, err := jsonShaped(v)
	if err != nil {
		return Selection{}, fmt.Errorf("SelectFields: %w", err)
	}
	var atPath []string
	if at != "" {
		if atPath, err = dottedPath(at); err != nil {
			return Selection{}, fmt.Errorf("SelectFields: at %w", err)
		}
	}
	paths := make([][]string, len(fields))
	for i, f := range fields {
		if paths[i], err = dottedPath(f); err != nil {
			return Selection{}, fmt.Errorf("SelectFields: field %w", err)
		}
	}
	if data == nil {
		return Selection{from: from}, nil // null selects to null
	}
	out, err := selectAt(data, atPath, paths, "")
	if err != nil {
		return Selection{}, fmt.Errorf("SelectFields: at %q: %w", at, err)
	}
	return Selection{from: from, data: out}, nil
}

// jsonShaped returns v's type, pointers removed, and v as decoded JSON, numbers kept exact.
func jsonShaped(v any) (reflect.Type, any, error) {
	if s, ok := v.(Selection); ok {
		if s.from == nil {
			return nil, nil, errors.New("a zero Selection; make one with SelectFields")
		}
		return s.from, s.data, nil
	}
	t := reflect.TypeOf(v)
	if t == nil {
		return nil, nil, errors.New("no value")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // wrapped by the caller, which names the helper
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil {
		return nil, nil, err //nolint:wrapcheck // wrapped by the caller
	}
	return t, data, nil
}

// dottedPath splits a dotted path into its segments, none of them empty.
func dottedPath(p string) ([]string, error) {
	segs := strings.Split(p, ".")
	if slices.Contains(segs, "") {
		return nil, fmt.Errorf("%q has an empty segment", p)
	}
	return segs, nil
}

// selectAt copies node, reducing the items at the path at to fields. where names the place
// reached so far, for errors.
func selectAt(node any, at []string, fields [][]string, where string) (any, error) {
	switch n := node.(type) {
	case []any:
		out := make([]any, len(n))
		for i, e := range n {
			if e == nil {
				continue // a null item stays null
			}
			var err error
			if out[i], err = selectAt(e, at, fields, where); err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[string]any:
		if len(at) == 0 {
			return reduceItem(n, fields), nil
		}
		out := maps.Clone(n)
		if child, ok := n[at[0]]; ok && child != nil {
			sub, err := selectAt(child, at[1:], fields, strings.TrimPrefix(where+"."+at[0], "."))
			if err != nil {
				return nil, err
			}
			out[at[0]] = sub
		}
		return out, nil
	default:
		if where == "" {
			return nil, fmt.Errorf("the value is a %s, not an object or a list", jsonKind(node))
		}
		return nil, fmt.Errorf("%s is a %s, not an object or a list", where, jsonKind(node))
	}
}

// reduceItem keeps the fields of item that fields name.
func reduceItem(item map[string]any, fields [][]string) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		if picked, ok := pickField(item, f); ok {
			mergeFields(out, picked)
		}
	}
	return out
}

// pickField returns node reduced to the one field path names, or false when node lacks it. A
// list on the way keeps each element's field.
func pickField(node any, path []string) (any, bool) {
	if len(path) == 0 {
		return node, true
	}
	switch n := node.(type) {
	case map[string]any:
		child, ok := n[path[0]]
		if !ok {
			return nil, false
		}
		sub, ok := pickField(child, path[1:])
		if !ok {
			return nil, false
		}
		return map[string]any{path[0]: sub}, true
	case []any:
		out := make([]any, len(n))
		found := false
		for i, e := range n {
			sub, ok := pickField(e, path)
			if !ok {
				sub = map[string]any{}
			}
			out[i], found = sub, found || ok
		}
		return out, found
	}
	return nil, false
}

// mergeFields merges the picked field src into dst, joining nested objects and lists picked
// for two fields of the same parent (owner.login and owner.id).
func mergeFields(dst map[string]any, src any) {
	sm, ok := src.(map[string]any)
	if !ok {
		return
	}
	for k, v := range sm {
		dst[k] = mergeValue(dst[k], v)
	}
}

func mergeValue(dst, src any) any {
	switch s := src.(type) {
	case map[string]any:
		if d, ok := dst.(map[string]any); ok {
			mergeFields(d, s)
			return d
		}
	case []any:
		if d, ok := dst.([]any); ok && len(d) == len(s) {
			for i := range s {
				d[i] = mergeValue(d[i], s[i])
			}
			return d
		}
	}
	return src
}

// jsonKind names a decoded JSON value's kind.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case json.Number:
		return "number"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	default:
		return "object"
	}
}

// nativeNumbers copies a JSON-shaped value with each json.Number as an int64, a uint64 or a
// float64, which the yaml and toml encoders write as numbers.
func nativeNumbers(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if u, err := strconv.ParseUint(n.String(), 10, 64); err == nil {
			return u
		}
		f, _ := strconv.ParseFloat(n.String(), 64) //nolint:errcheck // a decoded number always parses
		return f
	case []any:
		out := make([]any, len(n))
		for i, e := range n {
			out[i] = nativeNumbers(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, e := range n {
			out[k] = nativeNumbers(e)
		}
		return out
	}
	return v
}

// SortBy sorts items in place, stably, by the field named by its JSON property name (a dotted
// name reaches into nested objects), descending when desc. Numbers compare as numbers, strings
// that are all RFC 3339 times as times, other strings byte by byte, and false before true.
// Items lacking the field, or holding null, sort last in both directions. It returns an error,
// leaving items as they were, when an item is not an object, when the field holds an object or
// a list, or when items hold values of different kinds in it.
//
// It reads items through JSON, which is fine for the lists a command line prints.
func SortBy[E any](items []E, field string, desc bool) error {
	path, err := dottedPath(field)
	if err != nil {
		return fmt.Errorf("SortBy: field %w", err)
	}
	_, data, err := jsonShaped(items)
	if err != nil {
		return fmt.Errorf("SortBy: %w", err)
	}
	docs, _ := data.([]any) // a nil slice encodes as null
	keys := make([]any, len(docs))
	kind := ""
	for i, doc := range docs {
		if _, ok := doc.(map[string]any); !ok {
			return fmt.Errorf("SortBy: item %d is a %s, not an object", i, jsonKind(doc))
		}
		keys[i] = fieldValue(doc, path)
		k := jsonKind(keys[i])
		switch {
		case k == "null":
		case k == "list" || k == "object":
			return fmt.Errorf("SortBy: field %q holds a %s, which does not sort", field, k)
		case kind == "":
			kind = k
		case kind != k:
			return fmt.Errorf("SortBy: field %q holds both %ss and %ss", field, kind, k)
		}
	}
	compare := sortCompare(kind, keys)
	order := make([]int, len(docs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		na, nb := keys[a] == nil, keys[b] == nil
		switch {
		case na && nb:
			return 0
		case na:
			return 1
		case nb:
			return -1
		}
		c := compare(keys[a], keys[b])
		if desc {
			return -c
		}
		return c
	})
	sorted := slices.Clone(items)
	for i, j := range order {
		items[i] = sorted[j]
	}
	return nil
}

// fieldValue returns the value at path in doc, or nil when doc lacks it.
func fieldValue(doc any, path []string) any {
	for _, seg := range path {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = m[seg]
	}
	return doc
}

// sortCompare returns the comparison for sort keys of kind; keys are all of that kind or nil.
func sortCompare(kind string, keys []any) func(a, b any) int {
	switch kind {
	case "number":
		return func(a, b any) int { return compareNumbers(a.(json.Number), b.(json.Number)) } //nolint:forcetypeassert // checked by kind
	case "bool":
		return func(a, b any) int { return cmp.Compare(boolRank(a.(bool)), boolRank(b.(bool))) } //nolint:forcetypeassert // checked by kind
	case "string":
		times := map[string]time.Time{}
		for _, k := range keys {
			if s, ok := k.(string); ok {
				t, err := time.Parse(time.RFC3339Nano, s)
				if err != nil {
					times = nil
					break
				}
				times[s] = t
			}
		}
		if times != nil {
			return func(a, b any) int { return times[a.(string)].Compare(times[b.(string)]) } //nolint:forcetypeassert // checked by kind
		}
		return func(a, b any) int { return strings.Compare(a.(string), b.(string)) } //nolint:forcetypeassert // checked by kind
	}
	return func(any, any) int { return 0 }
}

// compareNumbers compares two JSON numbers, exactly when both are integers.
func compareNumbers(a, b json.Number) int {
	if x, err := a.Int64(); err == nil {
		if y, err := b.Int64(); err == nil {
			return cmp.Compare(x, y)
		}
	}
	x, _ := strconv.ParseFloat(a.String(), 64) //nolint:errcheck // a decoded number always parses
	y, _ := strconv.ParseFloat(b.String(), 64) //nolint:errcheck // a decoded number always parses
	return cmp.Compare(x, y)
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// partialSchema returns the JSON Schema document schema with every `required` keyword removed,
// so a value missing fields still matches. Only schema positions are visited, so a property
// that happens to be named required is kept.
func partialSchema(schema []byte) ([]byte, error) {
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the caller, which names the command
	}
	dropRequired(doc)
	return json.Marshal(doc) //nolint:wrapcheck // wrapped by the caller
}

// dropRequired removes `required` from the schema s and every schema below it.
func dropRequired(s any) {
	m, ok := s.(map[string]any)
	if !ok {
		return
	}
	if _, ok := m["required"].([]any); ok {
		delete(m, "required")
	}
	for _, key := range []string{"properties", "patternProperties", "definitions", "$defs"} {
		if sub, ok := m[key].(map[string]any); ok {
			for _, v := range sub {
				dropRequired(v)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "additionalItems", "not", "if", "then", "else", "contains", "propertyNames"} {
		dropRequired(m[key])
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "items", "prefixItems"} {
		if list, ok := m[key].([]any); ok {
			for _, v := range list {
				dropRequired(v)
			}
		}
	}
}
