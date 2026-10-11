package codegen

import "strings"

// Accessors for the input keys the spec lets take more than one shape. The generated spec
// type holds each as decoded (`any`); every reader goes through these. A malformed value,
// which validation rejects, reads as absent.

// negation reads `negatable`: whether the flag has negated forms, and the one custom form it
// declares in place of the derived `--no-<x>` ones ("" for the derived forms).
func negation(s *InputSchema) (on bool, name string) {
	if s == nil {
		return false, ""
	}
	switch v := s.Negatable.(type) {
	case bool:
		return v, ""
	case string:
		return v != "", v
	}
	return false, ""
}

// negatable reports whether a flag has negated forms.
func negatable(s *InputSchema) bool {
	on, _ := negation(s)
	return on
}

// declaredLayouts reads `layout` (a string or a list) as the list it declares, in order.
func declaredLayouts(s *InputSchema) []string {
	if s == nil {
		return nil
	}
	switch v := s.Layout.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if l, ok := e.(string); ok && l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	return nil
}

// relative reads `relative`: past, future, both, or "".
func relative(s *InputSchema) string {
	if s == nil {
		return ""
	}
	return s.Relative
}

// negatedForms returns a flag's negated identifiers: its custom form, else "--no-<x>" for each
// long identifier; none when it is not negatable.
func negatedForms(f FlagInput) []string {
	on, custom := negation(f.Schema)
	switch {
	case !on:
		return nil
	case custom != "":
		return []string{custom}
	}
	var out []string
	for _, id := range flagIdentifiers(f) {
		if long, ok := strings.CutPrefix(id, "--"); ok {
			out = append(out, "--no-"+long)
		}
	}
	return out
}
