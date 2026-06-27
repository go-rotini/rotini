package codegen

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// This file owns the `rotini validate` operation end-to-end: the session/file-level
// validation that the Processor drives, plus the machinery (schema validation, the
// version guard, and the rotini-specific rules the JSON Schema can't express).

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error

// Validate is a convenience over [Processor.Validate]: it builds a Processor for
// version and runs the validate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (tests).
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	return NewProcessor(version).Validate(specPath, confPath, watch, failMode, onValidate, nil)
}

// ─── session + file validation ────────────────────────────────────────────────.

// validate runs the validator phase over the loaded spec and conf (call load first):
// each file validates itself. Problems are aggregated via errors.Join, or — in fast
// mode — the first is returned. It returns nil when both are valid.
func (s *session) validate() error {
	fast := s.failFast()

	specErrs, specWarns := splitProblems(s.spec.validate())
	s.warnings = append(s.warnings, specWarns...)
	if fast && len(specErrs) > 0 {
		return specErrs[0]
	}

	confErrs, confWarns := splitProblems(s.conf.validate())
	s.warnings = append(s.warnings, confWarns...)
	if fast && len(confErrs) > 0 {
		return confErrs[0]
	}

	return errors.Join(append(specErrs, confErrs...)...)
}

// failFast reports whether validation should stop at the first problem. The --fail
// override (s.failMode) wins; otherwise the loaded conf's validate.fail is used. Only
// "fast" enables it — anything else collects every problem (the default).
func (s *session) failFast() bool {
	mode := s.failMode
	if mode == "" && s.conf != nil && s.conf.conf != nil && s.conf.conf.Validate != nil {
		mode = s.conf.conf.Validate.Fail
	}
	return mode == "fast"
}

// validate schema-validates the spec against its compiled schema on the raw JSON
// instance (so unknown-field rules fire), then — only when it is schema-valid — runs
// the rotini-specific rules and enforces the version guard. It returns every
// problem found, empty when the spec is valid.
func (l *specLoader) validate() []error {
	if problems := validateInstance("spec", l.instance, l.schema); len(problems) > 0 {
		locateProblems(problems, l.path, l.locate)
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(l.spec)...)
	}
	locateProblems(problems, l.path, l.locate) // positions any pointer-shaped problems
	problems = append(problems, validateComposedTree(l.spec, l.path, l.version)...)
	return problems
}

// validateComposedTree is the deep `$ref` descend (W8/D-W8.3): when the spec composes
// child specs, run the generator's own composer (resolveTree) over the WHOLE tree so
// `rotini validate` catches problems that only emerge once refs are followed — name/
// alias collisions across composition boundaries, cyclic or missing refs, and each
// composed spec's version. It reuses generate's exact compose logic (no separate walk),
// so validate and generate cannot drift. Best-effort: it needs a module (composed
// commands resolve to import paths) and only matters when refs are present, so a
// ref-less spec or a module-less context is skipped — leaving per-spec validation as-is.
func validateComposedTree(spec *Spec, specPath, version string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	root, name, err := findModule()
	if err != nil {
		return nil // no module: a composed CLI can't generate here anyway; not validate's error to raise
	}
	// The composer resolves refs + import paths relative to the CWD module; only run it
	// when the spec actually lives inside that module (else CWD ≠ the spec's project and
	// the relative refs/imports would be meaningless — leave it to a validate run from
	// the right place).
	absSpec, err1 := filepath.Abs(specPath)
	absRoot, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil ||
		(absSpec != absRoot && !strings.HasPrefix(absSpec, absRoot+string(filepath.Separator))) {
		return nil
	}
	if _, err := resolveTree(spec, specPath, name, version); err != nil {
		return []error{&problem{kind: "spec", loc: "composition", msg: err.Error()}}
	}
	return nil
}

// specHasRefs reports whether any command in the tree composes a child spec via $ref.
func specHasRefs(spec *Spec) bool {
	found := false
	walkCommands(spec, func(c *Command, _ string) {
		if c.Ref != "" {
			found = true
		}
	})
	return found
}

// validate schema-validates the conf against its compiled schema on the raw JSON
// instance when one was resolved, then — only when it is schema-valid — runs the
// rotini-specific conf rules and enforces the version guard. A default
// conf (no file) has nothing to validate. It returns every problem found.
func (l *confLoader) validate() []error {
	if l.path == "" {
		return nil
	}
	if problems := validateInstance("conf", l.instance, l.schema); len(problems) > 0 {
		locateProblems(problems, l.path, l.locate)
		return problems
	}

	var problems []error
	for _, rule := range confLints {
		problems = append(problems, rule(l.conf)...)
	}

	return problems
}

// ─── schema validation + the version guard ─────────────────────────────.

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

// validateInstance validates a document's raw JSON instance (so schema rules
// like additionalProperties:false see unknown fields) against the given
// compiled schema, returning one [*problem] per violation. The instance is the
// one the loader converted from its single read, so validation and generation
// always judge the same bytes. It returns nil when the document is valid.
func validateInstance(kind string, instance []byte, schema *jsonschema.Schema) []error {
	result, err := schema.Validate(instance)
	if err != nil {
		return []error{fmt.Errorf("validate %s: %w", kind, err)}
	}
	if result.Valid {
		return nil
	}

	problems := make([]error, 0, len(result.Errors))
	for i := range result.Errors {
		ve := &result.Errors[i]
		loc := ve.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		problems = append(problems, &problem{kind: kind, loc: loc, msg: ve.Message})
	}
	return problems
}
