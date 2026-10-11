package codegen

import (
	"slices"
	"strings"

	"github.com/go-rotini/rotini"
)

// enumValue is one member of a schema's `enum`, in either spec form: a plain value, or an
// object with a summary, aliases, or a hidden or deprecated marker.
type enumValue struct {
	Value             string
	Summary           string
	Aliases           []string
	DeprecatedAliases []string
	Hidden            bool
	Deprecated        string
	DeprecatedSince   string
	RemovedIn         string
	ReplacedBy        string
}

// detailed reports whether the member declares more than its value and summary.
func (v enumValue) detailed() bool {
	return len(v.Aliases) > 0 || v.Hidden || v.Deprecated != ""
}

// listed reports whether help's short list, completion and error messages offer the value:
// neither hidden nor deprecated.
func (v enumValue) listed() bool { return !v.Hidden && v.Deprecated == "" }

// The generated spec type holds each enum member as decoded: a string, or a map from any of
// the spec formats. Every reader goes through these accessors. A malformed member, which
// validation rejects, reads as the value "".

// enumValues reads enum members as values with their summaries.
func enumValues(members []any) []enumValue {
	if len(members) == 0 {
		return nil
	}
	out := make([]enumValue, 0, len(members))
	for _, m := range members {
		out = append(out, readEnumMember(m))
	}
	return out
}

// readEnumMember reads one enum member.
func readEnumMember(m any) enumValue {
	switch v := m.(type) {
	case string:
		return enumValue{Value: v}
	case map[string]any:
		str := func(k string) string { s, _ := v[k].(string); return s }
		hidden, _ := v["hidden"].(bool)
		return enumValue{
			Value: str("value"), Summary: str("summary"),
			Aliases: anyStrings(v["aliases"]), DeprecatedAliases: anyStrings(v["deprecated_aliases"]),
			Hidden: hidden, Deprecated: str("deprecated"), DeprecatedSince: str("deprecated_since"),
			RemovedIn: str("removed_in"), ReplacedBy: str("replaced_by"),
		}
	}
	return enumValue{}
}

// enumStrings reads enum members as their plain values, in order.
func enumStrings(members []any) []string {
	if len(members) == 0 {
		return nil
	}
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, readEnumMember(m).Value)
	}
	return out
}

// enumDescribed reports whether any enum member carries a summary.
func enumDescribed(members []any) bool {
	for _, m := range members {
		if readEnumMember(m).Summary != "" {
			return true
		}
	}
	return false
}

// plainEnum returns members as plain values, the form JSON Schema output carries. A hidden value
// is left out, as it is from every list, and aliases are never listed.
func plainEnum(members []any) []any {
	out := make([]any, 0, len(members))
	for _, m := range members {
		if v := readEnumMember(m); !v.Hidden {
			out = append(out, v.Value)
		}
	}
	return out
}

// flattenEnums rewrites, in the JSON Schema document v, every schema's enum to plain values,
// dropping the spec's summaries. Only schema positions are visited (the root, properties,
// items, additionalProperties, definitions and the allOf/anyOf/oneOf branches), so a property
// that happens to be named enum is left alone.
func flattenEnums(v any) {
	s, ok := v.(map[string]any)
	if !ok {
		return
	}
	if members, ok := s["enum"].([]any); ok {
		s["enum"] = plainEnum(members)
	}
	for _, key := range []string{"properties", "definitions"} {
		if m, ok := s[key].(map[string]any); ok {
			for _, sub := range m {
				flattenEnums(sub)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		switch sub := s[key].(type) {
		case map[string]any:
			flattenEnums(sub)
		case []any:
			for _, e := range sub {
				flattenEnums(e)
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if list, ok := s[key].([]any); ok {
			for _, e := range list {
				flattenEnums(e)
			}
		}
	}
}

// anyStrings reads a decoded list of strings; anything else reads as none.
func anyStrings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// enumDetailed reports whether any enum member declares more than its value: a summary,
// aliases, or a hidden or deprecated marker. Only then does generated code carry EnumValues.
func enumDetailed(members []any) bool {
	for _, m := range members {
		if v := readEnumMember(m); v.Summary != "" || v.detailed() {
			return true
		}
	}
	return false
}

// enumVisible returns the values a page or contract shows, in order: every value that is not
// hidden, deprecated ones included, since they are still accepted.
func enumVisible(members []any) []string {
	var out []string
	for _, v := range enumValues(members) {
		if !v.Hidden {
			out = append(out, v.Value)
		}
	}
	return out
}

// enumAliases maps each alias of an enum to the value it binds as; nil when there are none.
func enumAliases(members []any) map[string]string {
	var out map[string]string
	for _, v := range enumValues(members) {
		for _, a := range v.Aliases {
			if out == nil {
				out = map[string]string{}
			}
			out[a] = v.Value
		}
	}
	return out
}

// runtimeEnumValues is the runtime form of enum members, for lint's runtime checks: nil when no
// member declares more than its spelling.
func runtimeEnumValues(members []any) []rotini.EnumValue {
	if !enumDetailed(members) {
		return nil
	}
	vals := enumValues(members)
	out := make([]rotini.EnumValue, len(vals))
	for i, v := range vals {
		out[i] = rotini.EnumValue{
			Value: v.Value, Summary: v.Summary, Aliases: v.Aliases, Hidden: v.Hidden,
			Deprecated: v.Deprecated, DeprecatedAliases: v.DeprecatedAliases,
			DeprecatedSince: v.DeprecatedSince, RemovedIn: v.RemovedIn, ReplacedBy: v.ReplacedBy,
		}
	}
	return out
}

// enumUnlisted returns the hidden and deprecated values, which messages leave out; nil when
// there are none.
func enumUnlisted(members []any) []string {
	var out []string
	for _, v := range enumValues(members) {
		if !v.listed() {
			out = append(out, v.Value)
		}
	}
	return out
}

// Note says what a value declares beyond its summary, for man and markdown pages: its aliases
// and its deprecation, as "(also yml)" and "(deprecated since 1.4.0, removed in 2.0.0: …; use
// toml instead)"; "" when it declares neither.
func (v enumValue) Note() string {
	var parts []string
	if len(v.Aliases) > 0 {
		parts = append(parts, "(also "+strings.Join(v.Aliases, ", ")+")")
	}
	if v.Deprecated != "" {
		var when []string
		if v.DeprecatedSince != "" {
			when = append(when, "since "+v.DeprecatedSince)
		}
		if v.RemovedIn != "" {
			when = append(when, "removed in "+v.RemovedIn)
		}
		d := "deprecated"
		if len(when) > 0 {
			d += " " + strings.Join(when, ", ")
		}
		d += ": " + v.Deprecated
		if v.ReplacedBy != "" {
			d += "; use " + v.ReplacedBy + " instead"
		}
		parts = append(parts, "("+d+")")
	} else if len(v.DeprecatedAliases) > 0 {
		parts = append(parts, "("+strings.Join(v.DeprecatedAliases, ", ")+" deprecated)")
	}
	return strings.Join(parts, " ")
}

// listedEnum is the template helper `listed .Enum .EnumValues`: the values help's short list
// offers, leaving out deprecated ones (hidden ones are never on a page).
func listedEnum(enum []string, values []enumValue) []string {
	if len(values) == 0 {
		return enum
	}
	out := make([]string, 0, len(enum))
	for _, e := range enum {
		if !slices.ContainsFunc(values, func(v enumValue) bool { return v.Value == e && v.Deprecated != "" }) {
			out = append(out, e)
		}
	}
	return out
}

// describedEnum is the template helper `described .EnumValues`: whether any value has a
// summary, which help shows as a sub-row per value.
func describedEnum(values []enumValue) bool {
	return slices.ContainsFunc(values, func(v enumValue) bool { return v.Summary != "" })
}
