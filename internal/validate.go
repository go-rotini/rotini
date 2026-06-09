package internal

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// problem is a single schema-validation failure: the location of the
// offending value within the document and a human-readable message, tagged
// by document kind ("spec" or "conf").
type problem struct {
	kind string
	loc  string
	msg  string
}

func (e *problem) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// ValidateFn is the signature of [Validate]. A command handler can bind it under a registry
// key and fetch it as an injectable service, so tests substitute a double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error

// Validate is the DI-bound [ValidateFn]: the convenience entry that runs the
// processor's validate workflow (load → validate). It mirrors [Generate].
//
// specPath is the spec-file command argument (empty → the .rotini.spec.* discovered
// in the working directory). confPath is the -c/--config flag value (empty → the
// .rotini.conf.* discovered next to the spec). failMode is the --fail flag value
// ("fast" stops at the first problem, anything else collects); when empty it falls
// back to the conf's validate.fail, so the flag overrides the conf. version is the
// running rotini binary's version string ("vX.Y.Z" / "v0.0.0") for the $schema guard
// ("" → skipped).
//
// onValidate, which may be nil, is called after each pass with a "[HH:MM:SS] <took>"
// summary and that pass's error (nil when the spec and conf are valid); Validate
// prints nothing itself, so the caller reports results through it. When watch is
// false it runs a single pass and returns that pass's error — every problem
// aggregated via [errors.Join] (or the first in "fast" mode). When watch is true it
// validates once and then re-validates whenever the spec or conf changes, until
// interrupted with ctrl-c (SIGINT); there every pass goes to onValidate and watching
// continues, and only a failure to start watching is returned.
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	return runProcessorWorkflow(specPath, confPath, version, failMode, watch, (*processor).validatePass, onValidate)
}

// specLints is the ordered set of spec lints run after the spec is schema-valid. Adding a lint is a
// one-line append here; each stays a pure func(*Spec) []error for isolated testing. The order is
// observable (collect mode joins problems in order), so keep it stable.
var specLints = []func(*Spec) []error{
	lintImportConsistency,
	lintLocalTimeout,
	lintFlagGroups,
	lintFlagDependencies,
	lintDuplicateFlagIdentifiers,
	lintSchemaRefs,
}

// rotiniSchemaURLRe matches the recognized rotini `$schema` URL form and captures
// the X.Y.Z version segment. A URL that doesn't match (absent, a branch ref, a
// different host — i.e. the user deliberately pointed it elsewhere) yields no
// capture, and checkSchemaVersion skips it.
var rotiniSchemaURLRe = regexp.MustCompile(`^https://raw\.githubusercontent\.com/go-rotini/rotini/refs/tags/([0-9]+\.[0-9]+\.[0-9]+)/schema-(?:spec|conf)\.json$`)

// checkSchemaVersion enforces that a document's `$schema` targets the same rotini
// release as the running binary. version is the binary's bound version string
// ("vX.Y.Z" or "v0.0.0"); its leading "v" is stripped to get the "X.Y.Z" segment
// compared against the document's `$schema` version. The check is skipped when the
// version is empty/unknown, or when the document's `$schema` is absent or not the
// recognized rotini refs/tags/<VER> form. A present, recognized, mismatched
// `$schema` is a validation error.
func checkSchemaVersion(kind, docSchema, version string) error {
	want := strings.TrimPrefix(version, "v")
	if want == "" {
		return nil
	}
	m := rotiniSchemaURLRe.FindStringSubmatch(docSchema)
	if m == nil {
		return nil
	}
	if docVer := m[1]; docVer != want {
		return &problem{
			kind: kind,
			loc:  "$schema",
			msg:  fmt.Sprintf("targets schema version %s but this rotini is %s — update the $schema version (or your rotini install) so they match", docVer, want),
		}
	}
	return nil
}

// validateDocument reads the document at path and validates its raw JSON instance
// (so schema rules like additionalProperties:false see unknown fields) against the
// given compiled schema, returning one error per problem: a read/convert failure,
// or one [*problem] per schema violation. It returns nil when the document is valid.
func validateDocument(path, kind string, schema *jsonschema.Schema) []error {
	instance, err := toJSON(path)
	if err != nil {
		return []error{fmt.Errorf("%s file: %w", kind, err)}
	}
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
