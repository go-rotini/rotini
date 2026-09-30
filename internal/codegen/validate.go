package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	problems = append(problems, schemaBlockProblems(rs.json)...)
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
	case "pattern":
		// A regex is how the schema checks a value, not how anyone should learn what to write.
		// Every patterned key in rotini's schemas declares `examples` (a guard keeps it so), and
		// those are what the reader needs.
		if ex := schemaExamples(strings.TrimSuffix(ve.KeywordLocation, "/pattern")); len(ex) > 0 {
			return fmt.Sprintf("%s must look like %s", patternSubject(ve.InstanceLocation), quotedOrList(ex))
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

// patternSubject names the value a pattern failed on: its key, or for a list element the list's
// key and position ("identifiers[1]").
func patternSubject(instanceLocation string) string {
	leaf := pointerLeaf(instanceLocation)
	if _, err := strconv.Atoi(leaf); err == nil {
		parent := pointerLeaf(strings.TrimSuffix(instanceLocation, "/"+leaf))
		return fmt.Sprintf("%q item %s", parent, leaf)
	}
	return fmt.Sprintf("%q", leaf)
}

// schemaDocuments are rotini's two embedded schemas, parsed once, for reading keywords the
// validator does not report — the `examples` a pattern failure quotes.
var schemaDocuments = sync.OnceValue(func() []any {
	var docs []any
	for _, raw := range [][]byte{schemaSpecFileBytes, schemaConfFileBytes} {
		var doc any
		if err := json.Unmarshal(raw, &doc); err == nil {
			docs = append(docs, doc)
		}
	}
	return docs
})

// schemaExamples returns the `examples` of the schema node at a keyword location
// ("#/definitions/BaseSchema/properties/$ref"), looked up in either embedded schema.
func schemaExamples(location string) []string {
	path := strings.Split(strings.TrimPrefix(location, "#/"), "/")
	for _, doc := range schemaDocuments() {
		node := doc
		for _, seg := range path {
			seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
			switch n := node.(type) {
			case map[string]any:
				node = n[seg]
			case []any:
				i, err := strconv.Atoi(seg)
				if err != nil || i >= len(n) {
					node = nil
				} else {
					node = n[i]
				}
			default:
				node = nil
			}
		}
		if m, ok := node.(map[string]any); ok {
			if raw, ok := m["examples"].([]any); ok {
				out := make([]string, 0, len(raw))
				for _, e := range raw {
					if s, ok := e.(string); ok {
						out = append(out, s)
					}
				}
				return out
			}
		}
	}
	return nil
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
			noun := splitCamel(segs[i+1])
			if strings.ContainsRune("aeiou", rune(noun[0])) {
				return "an " + noun
			}
			return "a " + noun
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

// ─── strictness inside schema blocks ──────────────────────────────────────────.

// Every other object in a spec is closed with additionalProperties:false, so a misspelled key
// is reported with its position. The `schema:` blocks were not, and could not be: Schema and
// InputSchema inherit BaseSchema through allOf, and JSON Schema Draft 7 cannot combine allOf
// with additionalProperties:false (each branch would reject the other's keys). The schema's own
// comment said Go's DisallowUnknownFields enforced it at parse time — nothing ever set that
// option, so any key at all was silently accepted:
//
//	schema: { type: string, uniqueItems: true, totallyMadeUpKey: 42 }   // validated clean
//
// schemaBlockProblems closes the gap in the validate stage, where it produces the same
// positioned "unknown key" message the schema validator produces everywhere else.

// schemaBlockKeySets are the keys each kind of schema block may carry. They are DERIVED from the
// embedded spec schema — BaseSchema's properties plus each definition's own additions — so the
// check can never disagree with the schema it enforces.
type schemaBlockKeySets struct {
	input  map[string]bool // InputSchema: flags, arguments, env, config, stdin
	object map[string]bool // Schema: output, schemas, config_files, properties, items
}

var loadSchemaBlockKeys = sync.OnceValues(func() (schemaBlockKeySets, error) {
	var doc struct {
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			AllOf      []struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"allOf"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(schemaSpecFileBytes, &doc); err != nil {
		return schemaBlockKeySets{}, fmt.Errorf("read embedded spec schema: %w", err)
	}
	keysOf := func(def string) map[string]bool {
		out := map[string]bool{}
		for k := range doc.Definitions["BaseSchema"].Properties {
			out[k] = true
		}
		for _, branch := range doc.Definitions[def].AllOf {
			for k := range branch.Properties {
				out[k] = true
			}
		}
		return out
	}
	sets := schemaBlockKeySets{input: keysOf("InputSchema"), object: keysOf("Schema")}
	if len(sets.input) == 0 || len(sets.object) == 0 {
		return schemaBlockKeySets{}, errors.New("embedded spec schema has no BaseSchema/Schema/InputSchema properties")
	}
	return sets, nil
})

// jsonSchemaOnlyKeywords are JSON Schema keywords rotini's schema blocks do not implement. They
// get a sharper message than an ordinary typo, because the author wrote them expecting them to
// DO something — uniqueItems to deduplicate, format to validate — and silence would have told
// them it did.
var jsonSchemaOnlyKeywords = map[string]bool{
	"uniqueItems": true, "const": true, "format": true, "contains": true, "minContains": true,
	"maxContains": true, "additionalProperties": true, "patternProperties": true,
	"propertyNames": true, "minProperties": true, "maxProperties": true, "dependencies": true,
	"dependentRequired": true, "dependentSchemas": true, "if": true, "then": true, "else": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true, "additionalItems": true,
	"unevaluatedItems": true, "unevaluatedProperties": true, "$id": true, "$defs": true,
	"definitions": true, "title": true, "description": true, "examples": true, "$comment": true,
	"readOnly": true, "writeOnly": true, "deprecated": true, "contentMediaType": true,
	"contentEncoding": true, "default": true,
}

// schemaBlockProblems reports every key in a spec's schema blocks that the block's kind does not
// define, positioned by JSON pointer. It walks every place a schema block can appear — input
// schemas on each channel, output, the document-level schemas, config file schemas — and
// recurses through properties and items.
func schemaBlockProblems(instance []byte) []error {
	sets, err := loadSchemaBlockKeys()
	if err != nil {
		return []error{err}
	}
	var doc map[string]any
	if json.Unmarshal(instance, &doc) != nil {
		return nil // a malformed document is the schema validator's to report
	}
	w := &schemaBlockWalker{sets: sets}
	w.command(doc["command"], "/command")
	return w.problems
}

// schemaBlockWalker accumulates schemaBlockProblems' findings as it descends the command tree.
type schemaBlockWalker struct {
	sets     schemaBlockKeySets
	problems []error
}

// inputChannels are the command keys whose entries each carry an InputSchema under `schema`.
var inputChannels = []struct{ key, noun string }{
	{"flags", "a flag schema"}, {"arguments", "an argument schema"},
	{"env", "an env schema"}, {"config", "a config schema"},
}

// command checks every schema block one command owns, then its sub-commands.
func (w *schemaBlockWalker) command(node any, ptr string) {
	c, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, ch := range inputChannels {
		w.entrySchemas(c[ch.key], true, ptr+"/"+ch.key, ch.noun)
	}
	w.entrySchemas(c["config_files"], false, ptr+"/config_files", "a config file schema")
	if stdin, ok := c["stdin"].(map[string]any); ok {
		if s, ok := stdin["schema"]; ok {
			w.block(s, true, ptr+"/stdin/schema", "the stdin schema")
		}
	}
	if out, ok := c["output"]; ok {
		w.block(out, false, ptr+"/output", "the output schema")
	}
	if named, ok := c["schemas"].(map[string]any); ok {
		for _, n := range slices.Sorted(maps.Keys(named)) {
			w.block(named[n], false, ptr+"/schemas/"+escapePointer(n), "a named schema")
		}
	}
	subs, _ := c["commands"].([]any)
	for i, sub := range subs {
		w.command(sub, fmt.Sprintf("%s/commands/%d", ptr, i))
	}
}

// entrySchemas checks the `schema` of each entry in a list such as flags or config_files.
func (w *schemaBlockWalker) entrySchemas(list any, input bool, ptr, noun string) {
	entries, _ := list.([]any)
	for i, entry := range entries {
		if e, ok := entry.(map[string]any); ok {
			if s, ok := e["schema"]; ok {
				w.block(s, input, fmt.Sprintf("%s/%d/schema", ptr, i), noun)
			}
		}
	}
}

// block checks one schema block's keys, then recurses into its properties and items — which are
// always object schemas, whatever kind of block contains them.
func (w *schemaBlockWalker) block(node any, input bool, ptr, noun string) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	allowed := w.sets.object
	if input {
		allowed = w.sets.input
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !allowed[k] {
			w.problems = append(w.problems, &problem{kind: "spec", loc: ptr + "/" + escapePointer(k), msg: unknownSchemaKey(k, noun)})
		}
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for _, k := range slices.Sorted(maps.Keys(props)) {
			w.block(props[k], false, ptr+"/properties/"+escapePointer(k), "a property schema")
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		w.block(items, false, ptr+"/items", "an items schema")
	}
}

// unknownSchemaKey renders the message, sharpening it for a JSON Schema keyword the author
// clearly expected to work.
func unknownSchemaKey(key, noun string) string {
	msg := fmt.Sprintf("unknown key %q in %s", key, noun)
	if jsonSchemaOnlyKeywords[key] {
		msg += fmt.Sprintf(" — %q is a JSON Schema keyword rotini's schema blocks do not implement, so it would have done nothing", key)
	}
	return msg
}

// escapePointer escapes one JSON pointer segment (RFC 6901).
func escapePointer(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), "/", "~1")
}
