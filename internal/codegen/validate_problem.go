package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// problem is the shared finding currency of both the validate stage (schema/version
// findings, in validate.go) and the lint stage (the rotini-rule findings, in
// lint_spec.go / lint_conf.go), plus the helpers that partition and position them.

// severity classifies a validation problem. The zero value is an error (fails
// validation); a warning is surfaced separately but does NOT fail. The validate
// command routes the two to the funnel (as its errors and warnings respectively).
type severity int

const (
	severityError   severity = iota // zero value — fails validation
	severityWarning                 // advisory — surfaced, never fails
)

// problem is a single validation finding: the location of the offending value within
// the document and a human-readable message, tagged by document kind ("spec"/"conf")
// and severity (error by default; warning for non-fatal advisories).
type problem struct {
	kind string
	loc  string
	// ptr is the JSON pointer of the thing the problem is about, when the rule knows it.
	// A schema violation's loc IS a pointer and needs none; a lint rule's loc is a human
	// label ("command demo/build"), so it carries the pointer here instead and keeps the
	// label for the message. Either way locateProblems turns it into a file:line:col.
	ptr   string
	pos   string // "path:line:col" in the original source; "" degrades to loc-only
	msg   string
	sev   severity // zero value = error
	cause error    // optional typed error this problem carries, reachable via errors.As
}

// splitProblems separates a finding list into fatal errors and non-fatal
// warnings by each finding's severity. A non-*problem error counts as an error.
func splitProblems(problems []error) (errs, warns []error) {
	for _, e := range problems {
		var p *problem
		if errors.As(e, &p) && p.sev == severityWarning {
			warns = append(warns, e)
			continue
		}
		errs = append(errs, e)
	}
	return errs, warns
}

func (e *problem) Error() string {
	if e.pos != "" {
		return fmt.Sprintf("%s: %s: %s: %s", e.kind, e.pos, e.loc, e.msg)
	}
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// Unwrap exposes an optional typed cause so a caller's errors.As/Is reaches it
// through the aggregated validation error.
func (e *problem) Unwrap() error { return e.cause }

// locateProblems back-fills source positions, so a problem that knows where it came from gains
// "path:line:col". Two shapes qualify: a schema violation, whose loc IS a JSON pointer, and a
// lint problem carrying one in ptr beside its human label. A problem with neither passes
// through untouched, as do all problems when the source format carries no positions.
//
// A pointer that no longer resolves (a rule addressing a node the locator cannot find)
// degrades to a message without a position rather than failing — a lint problem worth
// reporting is still worth reporting unplaced.
func locateProblems(problems []error, path string, locate sourceLocator) {
	if locate == nil || path == "" {
		return
	}
	for _, e := range problems {
		p := &problem{}
		if !errors.As(e, &p) {
			continue
		}
		ptr := p.ptr
		if ptr == "" && strings.HasPrefix(p.loc, "/") {
			ptr = p.loc
		}
		if ptr == "" {
			continue
		}
		if line, col, ok := locate(ptr); ok {
			p.pos = fmt.Sprintf("%s:%d:%d", path, line, col)
		}
	}
}
