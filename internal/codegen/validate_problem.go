package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// severity classifies a problem. The zero value is an error, which fails validation; a
// warning is reported but does not fail.
type severity int

const (
	severityError severity = iota
	severityWarning
)

// problem is a single validate- or lint-stage finding: a message about a location in a
// spec or conf document.
type problem struct {
	kind string // "spec" or "conf"
	loc  string
	// ptr is the JSON pointer of the subject when loc is a human label (lint rules). A
	// schema violation's loc is itself a pointer. locateProblems resolves either to pos.
	ptr   string
	pos   string // "path:line:col" in the source, or just "path" when unplaceable
	msg   string
	sev   severity
	cause error // optional typed cause, reachable via errors.As
}

// splitProblems partitions problems into errors and warnings by severity. A non-*problem
// error counts as an error.
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

// Unwrap returns the optional typed cause.
func (e *problem) Unwrap() error { return e.cause }

// locateProblems sets each problem's pos to "path:line:col" by resolving its pointer (ptr,
// or loc when loc is a pointer) to the pointer's nearest locatable node. A problem that
// cannot be placed still gets pos = path so the file is named. It does nothing when the
// source has no locator or path.
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
		if line, col, ok := locateNearest(locate, ptr); ok {
			p.pos = fmt.Sprintf("%s:%d:%d", path, line, col)
			continue
		}
		p.pos = path
	}
}

// locateNearest resolves ptr, or else its nearest resolvable ancestor (a locator may be unable
// to place some keys, such as inside a TOML inline table). The document root is never
// resolved, as it has no position of its own.
func locateNearest(locate sourceLocator, ptr string) (line, col int, ok bool) {
	for ptr != "" && ptr != "/" {
		if line, col, ok := locate(ptr); ok {
			return line, col, true
		}
		i := strings.LastIndex(ptr, "/")
		if i <= 0 {
			break
		}
		ptr = ptr[:i]
	}
	return 0, 0, false
}
