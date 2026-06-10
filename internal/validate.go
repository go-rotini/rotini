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

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error)) error

// Validate is a convenience over [Processor.Validate]: it builds a Processor for
// version and runs the validate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (tests).
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	return NewProcessor(version).Validate(specPath, confPath, watch, failMode, onValidate)
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
