package codegen

import (
	"fmt"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// This file owns the validate STAGE of the pipeline: validateSpec/validateConf (the
// version check + JSON-Schema validation the Processor runs before lint) plus the
// schema-validation helper validateInstance. The shared problem/finding machinery is
// problem.go; the lint stage lives in lint_spec.go / lint_conf.go; the deep
// composed-$ref check is lint_compose.go.

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error

// ─── validate (version + schema) ───────────────────────────────────────────────.

// validateSpec is the validate stage for the spec: it checks the spec targets THIS
// rotini (its `version` matches the binary) and is schema-valid against the embedded
// spec schema on the canonical-JSON instance (so unknown-field rules fire), returning
// every problem positioned to source. It does NOT lint — that is lintSpec, run only
// when this passes (the lint rules assume a schema-valid shape).
func (p *Processor) validateSpec(rs *reconciledSpec) []error {
	var problems []error
	if vp := versionProblem("spec", rs.spec.Version, p.version); vp != nil {
		problems = append(problems, vp)
	}
	problems = append(problems, validateInstance("spec", rs.json, p.specSchema)...)
	locateProblems(problems, rs.path, rs.locate)
	return problems
}

// validateConf is validateSpec for the conf. A defaulted conf (no file) has nothing to
// validate and returns no problems.
func (p *Processor) validateConf(rc *reconciledConf) []error {
	if rc.path == "" {
		return nil
	}
	var problems []error
	if vp := versionProblem("conf", rc.conf.Version, p.version); vp != nil {
		problems = append(problems, vp)
	}
	problems = append(problems, validateInstance("conf", rc.json, p.confSchema)...)
	locateProblems(problems, rc.path, rc.locate)
	return problems
}

// versionProblem reports a spec/conf whose `version` targets a different rotini than the
// running binary. The `version` key carries the rotini version the document targets;
// codegen and validation are only reliable when it matches this binary, so a mismatch is
// a fatal problem. Skipped when the binary version is unknown ("" — a dev/test build) or
// the document declares none (the schema requires one, so this is belt-and-suspenders).
func versionProblem(kind, docVersion, binaryVersion string) *problem {
	want := strings.TrimPrefix(binaryVersion, "v")
	if want == "" {
		return nil
	}
	got := strings.TrimPrefix(docVersion, "v")
	if got == "" {
		return nil
	}
	if got != want {
		return &problem{
			kind: kind, loc: "version",
			msg: fmt.Sprintf("targets rotini version %s but this rotini is %s — update the version (or your rotini install) so they match", got, want),
		}
	}
	return nil
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
