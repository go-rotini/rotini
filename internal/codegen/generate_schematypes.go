package codegen

import (
	"maps"
	"strings"
)

// jsonSchemaTypes rewrites, in place, each `type` written as a rotini type name (int, bool,
// []string, duration) into its JSON Schema spelling, which the spec treats as equivalent, and
// returns v. Any other type, such as a named or imported Go type, is left as written.
func jsonSchemaTypes(v any) any {
	t, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, val := range t {
		switch k {
		case "properties", "definitions":
			if m, ok := val.(map[string]any); ok {
				for name, sub := range m {
					m[name] = jsonSchemaTypes(sub)
				}
			}
		case "items", "additionalProperties":
			t[k] = jsonSchemaTypes(val)
		case "allOf", "anyOf", "oneOf":
			if list, ok := val.([]any); ok {
				for i := range list {
					list[i] = jsonSchemaTypes(list[i])
				}
			}
		}
	}
	if typ, ok := t["type"].(string); ok && rotiniTypeName(typ) {
		delete(t, "type")
		maps.Copy(t, standardType(typ, t))
	}
	return t
}

// rotiniTypeName reports whether typ is a type name standardType maps that isn't already JSON
// Schema's, element types included.
func rotiniTypeName(typ string) bool {
	switch typ {
	case "string", "integer", "number", "boolean", "object", "array", "null":
		return false
	}
	var known func(string) bool
	known = func(t string) bool {
		if elem, ok := strings.CutPrefix(t, "[]"); ok {
			return known(elem)
		}
		if _, val, ok := splitMapType(t); ok {
			return known(val)
		}
		return len(standardType(t, map[string]any{})) > 0
	}
	return known(typ)
}
