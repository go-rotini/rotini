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

// semver is a document's or binary's X.Y.Z, with any -prerelease/+build suffix discarded.
type semver struct{ major, minor, patch int }

// parseSemver reads a leading X.Y.Z, tolerating a "v" prefix and ignoring anything after the
// patch number (a "-rc.1" or "+build" suffix). ok is false for anything else — a dev build
// stamped "dev", an empty string — which the version check treats as "unknown, do not judge".
func parseSemver(v string) (semver, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" {
			return semver{}, false
		}
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return semver{}, false
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return semver{major: out[0], minor: out[1], patch: out[2]}, true
}

// String renders the version back as X.Y.Z.
func (v semver) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

// olderThan reports whether v precedes w.
func (v semver) olderThan(w semver) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// versionProblem reports a document this rotini cannot be trusted to process.
//
// A document's `version` is a MINIMUM, not an equality: it says "I use the rotini feature set
// as of X.Y.Z". Any binary of the same major that is at least that version accepts it, so a
// patch or minor upgrade never forces an edit to a single spec or conf in a fleet. Two cases
// are still errors, because in both the binary genuinely cannot be relied on:
//
//   - the binary is OLDER than the document — the document may use keys it does not know, and
//     the schema would reject them with a confusing "unknown property" instead of the truth;
//   - the majors differ — by definition a different, incompatible feature set.
//
// It is skipped whenever either side is not a parseable X.Y.Z: a dev build with no version
// stamped in, or a document that declares none. Judging an unknown is worse than not judging.
func versionProblem(kind, docVersion, binaryVersion string) *problem {
	bin, ok := parseSemver(binaryVersion)
	if !ok {
		return nil
	}
	doc, ok := parseSemver(docVersion)
	if !ok {
		return nil
	}

	switch {
	case doc.major != bin.major:
		return &problem{
			kind: kind, loc: "version",
			msg: fmt.Sprintf("targets rotini %s but this rotini is %s — major version %d and %d are different, incompatible feature sets; install rotini %d.x or migrate this document to %d.x",
				doc, bin, doc.major, bin.major, doc.major, bin.major),
		}
	case bin.olderThan(doc):
		return &problem{
			kind: kind, loc: "version",
			msg: fmt.Sprintf("targets rotini %s but this rotini is %s — this rotini is older than the document requires; upgrade it (go get -tool github.com/go-rotini/rotini@latest), or lower the version to %s if the document does not use anything newer",
				doc, bin, bin),
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
