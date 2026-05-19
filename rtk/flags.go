package rtk

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// isFlag reports whether a token looks like a flag — starts with `-` and is
// longer than a single dash. A bare `-` is reserved for the stdin marker.
func isFlag(token string) bool {
	return len(token) > 1 && token[0] == '-'
}

// splitFlagValue splits a flag token on the first `=`, returning the flag
// identifier, the inline value (if any), and whether an inline value was
// present.
//
// Examples:
//
//	splitFlagValue("--output") = ("--output", "", false)
//	splitFlagValue("--output=json") = ("--output", "json", true)
//	splitFlagValue("-o=json") = ("-o", "json", true)
func splitFlagValue(token string) (name, value string, hasValue bool) {
	return strings.Cut(token, "=")
}

// findFlag returns a pointer to the FlagSpec whose Identifiers contain
// identifier, or nil if no such flag exists in flags.
func findFlag(flags []FlagSpec, identifier string) *FlagSpec {
	for i := range flags {
		if slices.Contains(flags[i].Identifiers, identifier) {
			return &flags[i]
		}
	}
	return nil
}

// expandGroupedShortFlags expands a grouped short-flag token like "-abc"
// into individual flag pointers, one per character. Returns nil if any
// character does not match a known short flag or if any of the matched
// flags is non-bool (grouped shorts are only allowed for bool flags).
//
// The token must start with a single dash and contain at least two
// characters after it; otherwise grouped-expansion does not apply and the
// caller should treat the token as a normal flag.
func expandGroupedShortFlags(token string, flags []FlagSpec) []*FlagSpec {
	if len(token) < 3 || token[0] != '-' || token[1] == '-' {
		return nil
	}

	chars := token[1:]
	metas := make([]*FlagSpec, len(chars))
	for ci, ch := range chars {
		id := "-" + string(ch)
		meta := findFlag(flags, id)
		if meta == nil {
			return nil
		}
		if meta.Type != "bool" {
			return nil
		}
		metas[ci] = meta
	}
	return metas
}

// appendFlagValue accumulates value into the scope under flagName.
//
// For map types (map[string]string, map[string]int, map[string]bool,
// map[string]float64), the value is merged into the existing map under
// flagName.
//
// For every other type, repeated invocations append the values into a
// []any slice — preserving the rotiniold behavior where multiple
// occurrences of the same flag collect into a list. (Codegen-emitted
// PopulateFromArgv asserts back to the declared concrete slice type.)
func appendFlagValue(scope map[string]any, flagName string, value any) {
	switch m := value.(type) {
	case map[string]string:
		if existing, ok := scope[flagName]; ok {
			if em, ok := existing.(map[string]string); ok {
				maps.Copy(em, m)
				return
			}
		}
		scope[flagName] = m
		return
	case map[string]int:
		if existing, ok := scope[flagName]; ok {
			if em, ok := existing.(map[string]int); ok {
				maps.Copy(em, m)
				return
			}
		}
		scope[flagName] = m
		return
	case map[string]bool:
		if existing, ok := scope[flagName]; ok {
			if em, ok := existing.(map[string]bool); ok {
				maps.Copy(em, m)
				return
			}
		}
		scope[flagName] = m
		return
	case map[string]float64:
		if existing, ok := scope[flagName]; ok {
			if em, ok := existing.(map[string]float64); ok {
				maps.Copy(em, m)
				return
			}
		}
		scope[flagName] = m
		return
	}

	if existing, ok := scope[flagName]; ok {
		if slice, ok := existing.([]any); ok {
			scope[flagName] = append(slice, value)
			return
		}
	}
	scope[flagName] = []any{value}
}

// resolveFlagValue determines the typed value for one occurrence of a flag,
// given the parse state.
//
// inlineValue / hasInlineValue describe a `--flag=value`-form token; args
// and idx describe the position when the value comes from the next argv
// token (`--flag value` form). The bool flag handles `--flag` (no value)
// and `--flag true|false` shorthands.
//
// Returns:
//   - the coerced value
//   - whether the next argv token was consumed as the value
//   - any coercion / validation error
func resolveFlagValue(meta *FlagSpec, flagName, inlineValue string, hasInlineValue bool, args []string, idx int, coerce CoerceValueFn) (any, bool, error) {
	if hasInlineValue {
		val, _, err := coerce(meta.Type, inlineValue)
		if err != nil {
			return nil, false, &CoercionError{Field: meta.Name, Kind: "flag", Value: inlineValue, Type: meta.Type, Cause: err}
		}
		if err := validateEnumValue(meta, inlineValue); err != nil {
			return nil, false, err
		}
		if err := validateFlagConstraints(meta, inlineValue, val); err != nil {
			return nil, false, err
		}
		return val, false, nil
	}

	if meta.Type == "bool" {
		if idx+1 < len(args) {
			switch args[idx+1] {
			case "true":
				return true, true, nil
			case "false":
				return false, true, nil
			}
		}
		return true, false, nil
	}

	if idx+1 >= len(args) {
		return nil, false, fmt.Errorf("flag %q requires a value", flagName)
	}
	rawVal := args[idx+1]
	if err := validateEnumValue(meta, rawVal); err != nil {
		return nil, false, err
	}
	val, _, err := coerce(meta.Type, rawVal)
	if err != nil {
		return nil, false, &CoercionError{Field: meta.Name, Kind: "flag", Value: rawVal, Type: meta.Type, Cause: err}
	}
	if err := validateFlagConstraints(meta, rawVal, val); err != nil {
		return nil, false, err
	}
	return val, true, nil
}

// validateEnumValue checks whether value is one of the allowed enum
// entries for the flag. Returns nil if Enum is empty (no enum restriction).
func validateEnumValue(meta *FlagSpec, value string) error {
	if len(meta.Enum) == 0 {
		return nil
	}
	if slices.Contains(meta.Enum, value) {
		return nil
	}
	return &ValidationError{
		Field:      meta.Name,
		Kind:       "flag",
		Constraint: "enum",
		Value:      value,
		Message:    fmt.Sprintf("value %q is not one of the allowed values: %s", value, strings.Join(meta.Enum, ", ")),
	}
}

// validateFlagConstraints enforces min/max (numeric), minLength/maxLength
// (string), pattern (regex), and minItems/maxItems (slice) constraints
// declared on the FlagSpec against a parsed value.
func validateFlagConstraints(meta *FlagSpec, rawValue string, parsed any) error {
	switch meta.Type {
	case "int", "uint", "int32", "int64", "uint32", "uint64":
		n := toFloat(parsed)
		if meta.Min != nil && n < *meta.Min {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "min", Value: rawValue,
				Message: fmt.Sprintf("value %s must be >= %g", rawValue, *meta.Min)}
		}
		if meta.Max != nil && n > *meta.Max {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "max", Value: rawValue,
				Message: fmt.Sprintf("value %s must be <= %g", rawValue, *meta.Max)}
		}
	case "float", "float64":
		var n float64
		if v, ok := parsed.(float64); ok {
			n = v
		}
		if meta.Min != nil && n < *meta.Min {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "min", Value: rawValue,
				Message: fmt.Sprintf("value %s must be >= %g", rawValue, *meta.Min)}
		}
		if meta.Max != nil && n > *meta.Max {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "max", Value: rawValue,
				Message: fmt.Sprintf("value %s must be <= %g", rawValue, *meta.Max)}
		}
	case "string":
		s := rawValue
		if meta.MinLength != nil && len(s) < *meta.MinLength {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "minLength", Value: rawValue,
				Message: fmt.Sprintf("value %q length %d is less than minimum %d", s, len(s), *meta.MinLength)}
		}
		if meta.MaxLength != nil && len(s) > *meta.MaxLength {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "maxLength", Value: rawValue,
				Message: fmt.Sprintf("value %q length %d exceeds maximum %d", s, len(s), *meta.MaxLength)}
		}
		if meta.Pattern != "" {
			matched, err := regexp.MatchString("^(?:"+meta.Pattern+")$", s)
			if err != nil {
				return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "pattern", Value: rawValue,
					Message: fmt.Sprintf("pattern %q is invalid: %v", meta.Pattern, err)}
			}
			if !matched {
				return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "pattern", Value: rawValue,
					Message: fmt.Sprintf("value %q does not match pattern %s", s, meta.Pattern)}
			}
		}
	case "[]string", "[]int", "[]float", "[]float64", "[]bool":
		count := sliceLen(parsed)
		if meta.MinItems != nil && count < *meta.MinItems {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "minItems", Value: rawValue,
				Message: fmt.Sprintf("requires at least %d item(s), got %d", *meta.MinItems, count)}
		}
		if meta.MaxItems != nil && count > *meta.MaxItems {
			return &ValidationError{Field: meta.Name, Kind: "flag", Constraint: "maxItems", Value: rawValue,
				Message: fmt.Sprintf("allows at most %d item(s), got %d", *meta.MaxItems, count)}
		}
	}
	return nil
}

// validateRequiredFlags returns a MissingRequiredError if any flag declared
// Required is missing from scope.
func validateRequiredFlags(flags []FlagSpec, scope map[string]any) error {
	var missing []string
	for i := range flags {
		f := &flags[i]
		if !f.Required {
			continue
		}
		if _, ok := scope[f.Name]; ok {
			continue
		}
		if len(f.Identifiers) > 0 {
			missing = append(missing, f.Identifiers[0])
		} else {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		return &MissingRequiredError{Kind: "flag", Names: missing}
	}
	return nil
}

// toFloat collapses any numeric Go type into a float64 for comparison
// against FlagSpec.Min / FlagSpec.Max. Unrecognized types return 0.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case float32:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

// sliceLen reports the length of v when v is one of the supported slice
// types ([]string, []int, []float64, []bool). Returns 0 otherwise.
func sliceLen(v any) int {
	switch s := v.(type) {
	case []string:
		return len(s)
	case []int:
		return len(s)
	case []float64:
		return len(s)
	case []bool:
		return len(s)
	}
	return 0
}
