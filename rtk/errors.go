package rtk

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownType is returned by [CoerceBuiltinValue] when the requested type
// is not a known built-in. Custom coercers can detect this and dispatch to
// user-supplied logic.
var ErrUnknownType = errors.New("rtk: unknown built-in flag type")

// UnknownCommandError is returned when a command token does not match any
// registered command name or alias.
type UnknownCommandError struct {
	// Command is the unrecognized token.
	Command string

	// Parent is the parent command in whose subcommand list the lookup
	// failed. Empty for top-level lookups.
	Parent string

	// Suggestions lists nearby commands (by Levenshtein distance) the user
	// may have meant. At most one entry today.
	Suggestions []string
}

func (e *UnknownCommandError) Error() string {
	msg := fmt.Sprintf("unknown command %q", e.Command)
	if len(e.Suggestions) > 0 {
		msg += fmt.Sprintf(" -- did you mean %q?", e.Suggestions[0])
	}
	return msg
}

// UnknownFlagError is returned when a flag identifier is not recognized at
// the current command scope or any ancestor scope.
type UnknownFlagError struct {
	// Flag is the unrecognized identifier (including any leading dashes).
	Flag string

	// Command is the command at whose scope the lookup happened. Empty for
	// root-scope lookups.
	Command string

	// Suggestions lists nearby flag identifiers the user may have meant.
	Suggestions []string
}

func (e *UnknownFlagError) Error() string {
	msg := fmt.Sprintf("unknown flag %q", e.Flag)
	if e.Command != "" {
		msg += fmt.Sprintf(" for command %q", e.Command)
	}
	if len(e.Suggestions) > 0 {
		msg += fmt.Sprintf(" -- did you mean %q?", e.Suggestions[0])
	}
	return msg
}

// UnknownArgumentError is returned when more positional arguments are
// supplied than the active command declares.
type UnknownArgumentError struct {
	// Value is the surplus argument token.
	Value string

	// Position is the zero-based position of the surplus argument.
	Position int

	// Command is the command at which the surplus argument was supplied.
	Command string
}

func (e *UnknownArgumentError) Error() string {
	return fmt.Sprintf("unexpected argument %q at position %d for command %q", e.Value, e.Position, e.Command)
}

// MissingRequiredError is returned when one or more required flags or
// arguments are not supplied by any resolution source (argv, env, config,
// default).
type MissingRequiredError struct {
	// Kind is "flag" or "argument".
	Kind string

	// Names are the identifiers (first identifier for flags; argument names
	// for arguments) that are missing.
	Names []string
}

func (e *MissingRequiredError) Error() string {
	plural := "s"
	if len(e.Names) == 1 {
		plural = ""
	}
	return fmt.Sprintf("required %s%s not provided: %s", e.Kind, plural, strings.Join(e.Names, ", "))
}

// CoercionError is returned when a flag or argument value cannot be coerced
// into its declared type.
type CoercionError struct {
	// Field is the flag/argument name (the canonical Name, not an identifier).
	Field string

	// Kind is "flag" or "argument".
	Kind string

	// Value is the raw string value that failed coercion.
	Value string

	// Type is the target Go type.
	Type string

	// Cause is the underlying coercion error (typically from strconv or fmt).
	Cause error
}

func (e *CoercionError) Error() string {
	return fmt.Sprintf("invalid value %q for %s %q (expected %s): %v", e.Value, e.Kind, e.Field, e.Type, e.Cause)
}

func (e *CoercionError) Unwrap() error { return e.Cause }

// ValidationError is returned when a flag or argument value fails a
// declared spec constraint (enum / pattern / min / max / minLength /
// maxLength / minItems / maxItems).
type ValidationError struct {
	// Field is the flag/argument name.
	Field string

	// Kind is "flag" or "argument".
	Kind string

	// Constraint identifies the failing rule
	// (e.g., "enum", "pattern", "min", "max", "minLength").
	Constraint string

	// Value is the raw string value that failed validation.
	Value string

	// Message is a human-readable description of the failure.
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s %q: %s", e.Kind, e.Field, e.Message)
}
