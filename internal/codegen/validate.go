package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

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
		problems = append(problems, &problem{kind: kind, loc: loc, msg: humanizeSchemaError(ve)})
	}
	return problems
}

// humanizeSchemaError renders a schema violation in rotini's vocabulary instead of JSON
// Schema's, for the two failures whose stock wording says nothing to the person who caused
// them — and which happen to be the two mistakes everyone makes first:
//
//	/command/summry: schema is false; nothing matches   ->  unknown key "summry" on a command
//	/command: no anyOf branch matched                   ->  a command needs either "name" or "$ref"
//
// Everything else the validator says is already plain ("value is not of type array", "missing
// required property \"version\""), so it passes through untouched. rotini's own lint rules set
// the bar — they name the command, the input and the rule, and say why it matters — and these
// two were the only messages in the tool that failed it.
//
// Both readings are DERIVED, not hardcoded: the key comes from the instance pointer, the noun
// from the schema definition the keyword failed in, and the choice from the anyOf branches'
// own required-property causes. A schema change carries them along. The dispatch is on
// ve.Keyword, which the validator documents as the stable machine-readable classification —
// never on its message text.
func humanizeSchemaError(ve *jsonschema.ValidationError) string {
	switch ve.Keyword {
	case "false":
		// additionalProperties:false renders as a "false" schema for the offending key.
		key := pointerLeaf(ve.InstanceLocation)
		if key == "" {
			break
		}
		if noun := definitionNoun(ve.KeywordLocation); noun != "" {
			return fmt.Sprintf("unknown key %q on %s", key, noun)
		}
		return fmt.Sprintf("unknown key %q", key)
	case "type":
		// "value is not of type array" names the expectation and not the thing — so in
		// isolation, in a log or an editor's error list, it says which shape was wanted
		// without saying of what.
		if key := pointerLeaf(ve.InstanceLocation); key != "" {
			if want, ok := strings.CutPrefix(ve.Message, "value is not of type "); ok {
				return fmt.Sprintf("%q must be of type %s", key, want)
			}
		}
	case "anyOf":
		// Every branch failed. In rotini's schemas an anyOf is a choice between required
		// keys, so the branches' causes name the choice exactly.
		keys := requiredChoices(ve.Causes)
		if len(keys) < 2 {
			break
		}
		noun := definitionNoun(ve.KeywordLocation)
		if noun == "" {
			noun = "this value"
		}
		return fmt.Sprintf("%s needs either %s", noun, quotedOrList(keys))
	}
	return ve.Message
}

// pointerLeaf is the last segment of a JSON pointer ("/command/summry" -> "summry"), with
// pointer escapes undone. "" for the root pointer.
func pointerLeaf(pointer string) string {
	i := strings.LastIndex(pointer, "/")
	if i < 0 || i == len(pointer)-1 {
		return ""
	}
	seg := pointer[i+1:]
	seg = strings.ReplaceAll(seg, "~1", "/")
	return strings.ReplaceAll(seg, "~0", "~")
}

// definitionNoun turns a schema keyword location into the English noun for the shape that
// failed: "#/definitions/Command/anyOf" -> "a command", "#/definitions/FlagInput/..." ->
// "a flag input". "" when the location names no definition.
func definitionNoun(keywordLocation string) string {
	segs := strings.Split(strings.TrimPrefix(keywordLocation, "#/"), "/")
	for i, seg := range segs {
		if (seg == "definitions" || seg == "$defs") && i+1 < len(segs) {
			return "a " + splitCamel(segs[i+1])
		}
	}
	return ""
}

// splitCamel turns a PascalCase schema definition name into lowercase words:
// "RemoteCommandSpec" -> "remote command spec".
func splitCamel(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// requiredChoices collects the property each failed anyOf branch was missing, in branch order
// and deduped — the choice the author actually has.
func requiredChoices(causes []jsonschema.ValidationError) []string {
	var keys []string
	for i := range causes {
		c := &causes[i]
		if c.Keyword != "required" {
			return nil // not a required-key choice; say nothing rather than guess
		}
		key := betweenQuotes(c.Message)
		if key == "" {
			return nil
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// betweenQuotes extracts the first double-quoted run from s, or "".
func betweenQuotes(s string) string {
	_, rest, ok := strings.Cut(s, `"`)
	if !ok {
		return ""
	}
	inner, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return inner
}

// quotedOrList renders keys as `"a" or "b"` / `"a", "b" or "c"`.
func quotedOrList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = strconv.Quote(k)
	}
	switch len(quoted) {
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}
