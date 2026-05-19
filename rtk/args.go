package rtk

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// applyArgumentDefaults fills in default values for positional arguments
// not supplied on the CLI. It stops at the first variadic argument (which
// cannot have a "default" — variadic means "consume all remaining," and
// what's-remaining-when-nothing-was-supplied is just nothing).
func applyArgumentDefaults(argMetas []ArgumentSpec, args *[]string) {
	for i := range argMetas {
		a := &argMetas[i]
		if i < len(*args) {
			continue
		}
		if a.Variadic {
			break
		}
		if a.Default != "" {
			*args = append(*args, a.Default)
		}
	}
}

// validateRequiredArguments checks that every required positional argument
// is supplied. It also rejects excess positional tokens when the command
// has no variadic argument.
func validateRequiredArguments(argMetas []ArgumentSpec, providedArgs []string) error {
	requiredCount := 0
	hasVariadic := false
	for i := range argMetas {
		if argMetas[i].Required {
			requiredCount++
		}
		if argMetas[i].Variadic {
			hasVariadic = true
		}
	}

	if len(providedArgs) < requiredCount {
		var missing []string
		for i := range argMetas {
			if argMetas[i].Required && i >= len(providedArgs) {
				missing = append(missing, argMetas[i].Name)
			}
		}
		return &MissingRequiredError{Kind: "argument", Names: missing}
	}

	if !hasVariadic && len(providedArgs) > len(argMetas) {
		return fmt.Errorf("too many arguments: expected %d, got %d", len(argMetas), len(providedArgs))
	}

	return nil
}

// validateArgumentConstraints enforces enum and pattern constraints on a
// single positional argument value.
//
// Numeric / length / item-count constraints on arguments are handled by
// the coercion + flag-style validation path in the parser; this helper
// focuses on enum + pattern for backward compatibility with rotiniold's
// argument constraint surface.
func validateArgumentConstraints(meta *ArgumentSpec, value string) error {
	if len(meta.Enum) > 0 {
		if !slices.Contains(meta.Enum, value) {
			return &ValidationError{
				Field:      meta.Name,
				Kind:       "argument",
				Constraint: "enum",
				Value:      value,
				Message:    fmt.Sprintf("value %q is not one of the allowed values: %s", value, strings.Join(meta.Enum, ", ")),
			}
		}
	}
	if meta.Pattern != "" {
		matched, err := regexp.MatchString("^(?:"+meta.Pattern+")$", value)
		if err != nil {
			return &ValidationError{
				Field:      meta.Name,
				Kind:       "argument",
				Constraint: "pattern",
				Value:      value,
				Message:    fmt.Sprintf("pattern %q is invalid: %v", meta.Pattern, err),
			}
		}
		if !matched {
			return &ValidationError{
				Field:      meta.Name,
				Kind:       "argument",
				Constraint: "pattern",
				Value:      value,
				Message:    fmt.Sprintf("value %q does not match pattern %s", value, meta.Pattern),
			}
		}
	}
	return nil
}
