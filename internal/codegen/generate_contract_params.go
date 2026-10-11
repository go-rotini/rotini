package codegen

import (
	"fmt"
	"maps"
	"slices"
)

// withoutDeprecatedEnum returns an input's schema for callers that build a command line, such as
// the contract's parameters and tool definitions: its enum leaves out the values declared
// deprecated, which the program still accepts but nobody should start using. The enum sits on
// the schema, or on a list's items or a map's values. schema comes back unchanged when no value
// is deprecated, or when every value is.
func withoutDeprecatedEnum(schema any, values map[string]contractEnumValue) any {
	s, ok := schema.(map[string]any)
	if !ok || !slices.ContainsFunc(slices.Collect(maps.Values(values)), func(v contractEnumValue) bool { return v.Deprecated != "" }) {
		return schema
	}
	if enum, ok := s["enum"].([]any); ok {
		kept := slices.DeleteFunc(slices.Clone(enum), func(e any) bool { return values[fmt.Sprint(e)].Deprecated != "" })
		if len(kept) == 0 {
			return schema
		}
		s = maps.Clone(s)
		s["enum"] = kept
		return s
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if inner, ok := s[k].(map[string]any); ok && inner["enum"] != nil {
			s = maps.Clone(s)
			s[k] = withoutDeprecatedEnum(inner, values)
			return s
		}
	}
	return schema
}
