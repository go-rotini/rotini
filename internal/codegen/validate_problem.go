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
	kind  string
	loc   string
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

// locateProblems back-fills source positions onto pointer-shaped problems: a
// problem whose loc is a JSON-pointer instance location gains "path:line:col"
// when the document's locator can resolve it. Lint problems with semantic locs
// ("command app deploy") pass through untouched, as do all problems when the
// format carries no positions (TOML) — pointer-only is the documented degrade.
func locateProblems(problems []error, path string, locate sourceLocator) {
	if locate == nil || path == "" {
		return
	}
	for _, e := range problems {
		p := &problem{}
		ok := errors.As(e, &p)
		if !ok || !strings.HasPrefix(p.loc, "/") {
			continue
		}
		if line, col, ok := locate(p.loc); ok {
			p.pos = fmt.Sprintf("%s:%d:%d", path, line, col)
		}
	}
}
