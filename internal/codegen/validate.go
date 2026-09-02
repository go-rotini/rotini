package codegen

import (
	"fmt"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// The validate stage: the version check and JSON Schema validation the Processor runs before
// lint. The problem machinery is in validate_problem.go, the lint rules in lint_spec.go and
// lint_conf.go, and the deep composed-$ref check in lint_compose.go.

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error

// ─── validate (version + schema) ───────────────────────────────────────────────.

// validateSpec checks that the spec targets this rotini and is schema-valid against the
// embedded spec schema, validating the canonical-JSON instance so unknown-field rules fire,
// and returns every problem positioned to source. Linting is lintSpec's job, run only once
// this passes, since the lint rules assume a schema-valid shape.
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

// versionProblem reports a document whose `version` targets a different rotini than the
// running binary: codegen and validation are reliable only when the two match. It is skipped
// when the binary version is unknown (a dev build) or the document declares none.
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

// validateInstance validates a document's raw JSON instance — raw, so rules like
// additionalProperties:false see unknown fields — against the compiled schema, returning one
// [*problem] per violation. The instance came from the loader's single read, so validation and
// generation always judge the same bytes.
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
