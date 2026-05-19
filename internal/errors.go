package internal

import (
	"errors"
	"fmt"
	"strings"
)

// SpecError aggregates all validation failures for a single Spec into one
// error value. It implements [error] (via [SpecError.Error]) and supports
// [errors.Unwrap] / [errors.Is] / [errors.As] over its constituent
// issues.
//
// Validators accumulate issues into a SpecError rather than failing on
// the first problem so a single run surfaces every bad spec field at
// once. Empty SpecError ([Issues] is nil) is treated as "no errors";
// the validator returns nil instead in that case so callers can `err ==
// nil` check normally.
type SpecError struct {
	// Path is the dotted location of the spec the errors apply to
	// (e.g., "spec.yaml" or "<inline>"). Used for error-message context.
	Path string

	// Issues is the flat list of individual problems found.
	Issues []*SpecIssue
}

// Error returns a multiline summary suitable for printing to a terminal.
// The first line names the source; each subsequent line shows one issue.
func (e *SpecError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ""
	}
	var b strings.Builder
	plural := "s"
	if len(e.Issues) == 1 {
		plural = ""
	}
	if e.Path != "" {
		fmt.Fprintf(&b, "spec %s: %d issue%s\n", e.Path, len(e.Issues), plural)
	} else {
		fmt.Fprintf(&b, "spec: %d issue%s\n", len(e.Issues), plural)
	}
	for _, iss := range e.Issues {
		b.WriteString("  - ")
		b.WriteString(iss.Error())
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// Unwrap satisfies the Go 1.20+ multi-error convention, exposing each
// [SpecIssue] to [errors.Is] / [errors.As] walks.
func (e *SpecError) Unwrap() []error {
	if e == nil {
		return nil
	}
	out := make([]error, len(e.Issues))
	for i, iss := range e.Issues {
		out[i] = iss
	}
	return out
}

// add appends one issue. The caller passes location (a dotted path into
// the spec) and a fmt.Errorf-style message.
func (e *SpecError) addf(location, format string, args ...any) {
	e.Issues = append(e.Issues, &SpecIssue{
		Location: location,
		Message:  fmt.Sprintf(format, args...),
	})
}

// addKeyword is like [SpecError.add] but also records a stable
// machine-readable keyword (e.g., "duplicate", "pattern", "required").
// Callers reading SpecError programmatically can switch on Keyword to
// classify failures.
func (e *SpecError) addKeywordf(location, keyword, format string, args ...any) {
	e.Issues = append(e.Issues, &SpecIssue{
		Location: location,
		Keyword:  keyword,
		Message:  fmt.Sprintf(format, args...),
	})
}

// nonEmpty returns e when it carries at least one issue, otherwise nil.
// Validators use this at the end of their run so callers can do
// `if err := Validate(s); err != nil { ... }` even when SpecError is
// constructed up-front.
func (e *SpecError) nonEmpty() error {
	if e == nil || len(e.Issues) == 0 {
		return nil
	}
	return e
}

// SpecIssue is a single validation failure. Keyword carries the
// stable, machine-readable classification; Message is the human-readable
// description.
//
//nolint:errname // SpecIssue is the noun the package uses to describe one of N issues collected by SpecError; "SpecIssueError" reads as redundant.
type SpecIssue struct {
	// Location is the dotted path into the spec where the issue applies
	// (e.g., "commands[0].inputs.flags[1].identifiers[0]").
	Location string

	// Keyword is the stable classification of the failure (e.g.,
	// "schema", "duplicate", "pattern", "ref", "duration", "variadic").
	// Empty for issues that don't fit a standard classification.
	Keyword string

	// Message is the human-readable description.
	Message string
}

// Error returns a single-line summary of the issue.
func (i *SpecIssue) Error() string {
	switch {
	case i == nil:
		return ""
	case i.Location == "" && i.Keyword == "":
		return i.Message
	case i.Keyword == "":
		return fmt.Sprintf("[%s] %s", i.Location, i.Message)
	case i.Location == "":
		return fmt.Sprintf("(%s) %s", i.Keyword, i.Message)
	default:
		return fmt.Sprintf("[%s] (%s) %s", i.Location, i.Keyword, i.Message)
	}
}

// Is reports whether target is [ErrInvalidSpec]; this lets callers do
// `errors.Is(err, internal.ErrInvalidSpec)` to detect validation
// failures without depending on the concrete type.
func (i *SpecIssue) Is(target error) bool {
	return errors.Is(target, ErrInvalidSpec)
}

// ErrInvalidSpec is the sentinel for "validation failed." Both [*SpecError]
// and [*SpecIssue] match via [errors.Is].
var ErrInvalidSpec = errors.New("spec failed validation")

// Is on SpecError likewise honors the sentinel match.
func (e *SpecError) Is(target error) bool {
	return errors.Is(target, ErrInvalidSpec)
}
