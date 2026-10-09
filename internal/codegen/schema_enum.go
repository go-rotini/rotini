package codegen

// enumValue is one member of a schema's `enum`, in either spec form: a plain value, or a
// `{value, summary}` object.
type enumValue struct {
	Value   string
	Summary string
}

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
		value, _ := v["value"].(string)
		summary, _ := v["summary"].(string)
		return enumValue{Value: value, Summary: summary}
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

// plainEnum returns members as plain values, the form JSON Schema output carries.
func plainEnum(members []any) []any {
	out := make([]any, len(members))
	for i, m := range members {
		out[i] = readEnumMember(m).Value
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
