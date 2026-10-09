package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file derives the parsing facts the contract states beside each input's JSON Schema:
// the rotini type, the value kind, time layouts and the flag spellings a caller may type.

// schemaScope is the named schemas of one composed spec. A composed command's `$ref`s name
// its own spec's schemas, not the root's.
type schemaScope struct {
	name    string // the spec's root command name
	schemas map[string]Schema
}

// schemasOf returns the named schemas a command's `$ref`s resolve against.
func (p *program) schemasOf(scope *schemaScope) map[string]Schema {
	if scope == nil {
		return p.schemas
	}
	return scope.schemas
}

// lifecycle is a command's planned deprecation: the release that deprecated it, the release
// that removes it, and the release that removes each deprecated alias.
type lifecycle struct {
	since, removedIn string
	removedIDs       map[string]string
}

func lifecycleOf(c *Command) lifecycle {
	return lifecycle{since: c.DeprecatedSince, removedIn: c.RemovedIn, removedIDs: c.DeprecatedIdentifiersRemovedIn}
}

// Value kinds, as the contract names them: how a caller supplies the value.
const (
	kindScalar = "scalar" // one value
	kindCount  = "count"  // repeat the identifier; never `=N`
	kindList   = "list"   // repeat the flag, or split on its separator
	kindMap    = "map"    // key=value pairs
	kindObject = "object" // a named object schema, as JSON or field by field
)

// contractFacts are the facts about one input the contract states outside its JSON Schema.
type contractFacts struct {
	typ, kind     string
	layouts       []string
	separator     string
	from          []string
	implicitValue any
	ignoreCase    bool
	secret        bool
	configSource  string
	dottedKeys    bool
	nesting       string
	relative      string // past, future or both: the input also takes a time measured from now
	variableFile  string // the variable naming a file that holds the value
}

func contractFactsOf(s *InputSchema, schemas map[string]Schema) contractFacts {
	f := contractFacts{typ: contractType(s), kind: valueKind(s, schemas), layouts: timeLayouts(s)}
	if s != nil {
		f.separator, f.from, f.implicitValue = s.Separator, s.From, s.ImplicitValue
		f.ignoreCase, f.secret = s.IgnoreCase, s.Secret
		f.configSource, f.dottedKeys, f.nesting = s.ConfigSource, s.DottedKeys, s.Nesting
		f.relative, f.variableFile = s.Relative, s.VariableFile
	}
	return f
}

// contractType is an input's rotini type: a Go type (`int8`, `[]string`, `map[string]int`) or
// a rotini value type (`duration`, `date`, `count`), with the JSON Schema names written as the
// Go types they read into (`integer` is `int`). A type named by `$ref` gives "".
func contractType(s *InputSchema) string {
	if s == nil || (s.Type == "" && s.Ref == "") {
		return "string"
	}
	if s.Ref != "" {
		return ""
	}
	if s.Import != "" {
		return s.Type
	}
	if s.Type == "array" || s.Type == "[]string" {
		switch {
		case s.Items == nil || (s.Items.Type == "" && s.Items.Ref == ""):
			return "[]string"
		case s.Items.Ref != "":
			return ""
		default:
			return "[]" + canonicalType(s.Items.Type)
		}
	}
	return canonicalType(s.Type)
}

// canonicalType writes a spec type name the way the contract states it.
func canonicalType(t string) string {
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "[]" + canonicalType(elem)
	}
	if k, v, ok := splitMapType(t); ok {
		return "map[" + canonicalType(k) + "]" + canonicalType(v)
	}
	switch t {
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "array":
		return "[]string"
	case "object", "map":
		return "map[string]any"
	}
	return t
}

// valueKind is how a caller supplies an input's value; see the kind constants.
func valueKind(s *InputSchema, schemas map[string]Schema) string {
	switch t := getSchemaType(s); {
	case objectRef(s, schemas) != "":
		return kindObject
	case s != nil && s.Type == "count":
		return kindCount
	case strings.HasPrefix(t, "[]"):
		return kindList
	case strings.HasPrefix(t, "map["):
		return kindMap
	}
	return kindScalar
}

// timeLayouts is the layouts a time input is read with, tried in order (nil for any other
// input): the declared ones, else `2006-01-02` for a date, else RFC 3339.
func timeLayouts(s *InputSchema) []string {
	if s == nil {
		return nil
	}
	t := strings.TrimPrefix(getSchemaType(s), "[]")
	if _, v, ok := splitMapType(t); ok {
		t = v
	}
	if t != "time.Time" && t != "*time.Time" {
		return nil
	}
	if l := layoutsFor(s); len(l) > 0 {
		return l
	}
	return []string{time.RFC3339}
}

// timeFormat is the JSON Schema `format` every one of layouts writes, or "" when they don't
// all write the same one.
func timeFormat(layouts []string) string {
	format := ""
	for i, l := range layouts {
		var f string
		switch l {
		case time.RFC3339, time.RFC3339Nano:
			f = "date-time"
		case time.DateOnly:
			f = "date"
		}
		if f == "" || (i > 0 && f != format) {
			return ""
		}
		format = f
	}
	return format
}

// setTimeFormat sets a time input's format on the schema holding each value (the list's
// items or the map's values), or removes it when format is "".
func setTimeFormat(doc map[string]any, format string) {
	target := doc
	for _, k := range []string{"items", "additionalProperties"} {
		if m, ok := doc[k].(map[string]any); ok {
			delete(doc, "format")
			target = m
		}
	}
	if format == "" {
		delete(target, "format")
		return
	}
	target["format"] = format
}

// negatedFlagIdentifiers are the negated forms a negatable flag also accepts: its custom one,
// else a `--no-` form per long identifier. A form that a flag on the command declares as an
// identifier belongs to that flag, so it is left out.
func negatedFlagIdentifiers(f FlagInput, declared map[string]bool) []string {
	var out []string
	for _, id := range negatedForms(f) {
		if !declared[id] {
			out = append(out, id)
		}
	}
	return out
}

// writeLifecycleFields writes a Def literal's DeprecatedSince, RemovedIn and
// DeprecatedIdentifiersRemovedIn fields, each only when set, through format ("%s: %s" with the
// field name and its Go value).
func writeLifecycleFields(b *strings.Builder, format string, l lifecycle) {
	if l.since != "" {
		fmt.Fprintf(b, format, "DeprecatedSince", strconv.Quote(l.since))
	}
	if l.removedIn != "" {
		fmt.Fprintf(b, format, "RemovedIn", strconv.Quote(l.removedIn))
	}
	if len(l.removedIDs) > 0 {
		pairs := make([]string, 0, len(l.removedIDs))
		for _, id := range slices.Sorted(maps.Keys(l.removedIDs)) {
			pairs = append(pairs, strconv.Quote(id)+": "+strconv.Quote(l.removedIDs[id]))
		}
		fmt.Fprintf(b, format, "DeprecatedIdentifiersRemovedIn", "map[string]string{"+strings.Join(pairs, ", ")+"}")
	}
}
