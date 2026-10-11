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

// The validate stage: the version check and JSON Schema validation that run before lint.

// ValidateFn is the signature of [Processor.Validate]. The companion CLI injects it as a
// dependency so tests can substitute a double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode, release string, onValidate func(result string, err error), onWarnings func(warnings []error)) error

// validateSpec checks the spec's version and validates its canonical JSON against the
// embedded spec schema, returning every problem positioned in source. Lint runs only after
// this passes.
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

// validateConf is validateSpec for the conf. A defaulted conf (no file) returns no problems.
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

// parseSemver reads X.Y.Z, tolerating a "v" prefix and ignoring any -prerelease or +build
// suffix. ok is false for anything else, such as "dev" or "".
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

// versionProblem reports a document this rotini cannot process. A document's `version` is a
// minimum: any binary of the same major at or above it is accepted. It is an error when the
// majors differ or the binary is older than the document. The check is skipped when either
// version is unknown (unparseable, absent, or a 0.0.0 development build).
func versionProblem(kind, docVersion, binaryVersion string) *problem {
	bin, ok := parseSemver(binaryVersion)
	if !ok || bin == (semver{}) {
		return nil
	}
	doc, ok := parseSemver(docVersion)
	if !ok {
		return nil
	}

	switch {
	case doc.major != bin.major:
		return &problem{
			kind: kind, loc: "version", ptr: "/version",
			msg: fmt.Sprintf("targets rotini %s but this rotini is %s; major version %d and %d are different, incompatible feature sets; install rotini %d.x or migrate this document to %d.x",
				doc, bin, doc.major, bin.major, doc.major, bin.major),
		}
	case bin.olderThan(doc):
		return &problem{
			kind: kind, loc: "version", ptr: "/version",
			msg: fmt.Sprintf("targets rotini %s but this rotini is %s; this rotini is older than the document requires; upgrade it (go get -tool github.com/go-rotini/rotini/cmd/rotini@latest), or lower the version to %s if the document does not use anything newer",
				doc, bin, bin),
		}
	}
	return nil
}

// validateInstance validates a document's raw JSON instance against the compiled schema,
// returning one [*problem] per violation. Validating the raw instance lets
// additionalProperties:false see unknown fields.
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

// humanizeSchemaError rewrites a schema violation in rotini's vocabulary for the keywords
// whose stock wording is unhelpful:
//
//	false    /command/summry: schema is false  ->  unknown key "summry" on a command
//	type     value is not of type array        ->  "aliases" must be of type array
//	pattern  regex mismatch                    ->  "x" must look like <examples>
//	anyOf    no anyOf branch matched           ->  a command needs either "name" or "$ref"
//
// Wording is derived from the instance pointer and the schema itself (definition names,
// `examples`, `x-hint`, anyOf required causes), never hardcoded, and dispatch is on
// ve.Keyword rather than message text. Other messages pass through unchanged.
func humanizeSchemaError(ve *jsonschema.ValidationError) string {
	switch ve.Keyword {
	case "false":
		// additionalProperties:false renders as a "false" schema for the offending key.
		key := pointerLeaf(ve.InstanceLocation)
		if key == "" {
			break
		}
		if noun := definitionNoun(ve.KeywordLocation); noun != "" {
			msg := fmt.Sprintf("unknown key %q on %s", key, noun)
			if hint := schemaPlacementHint(definitionName(ve.KeywordLocation), key); hint != "" {
				msg += "; " + hint
			}
			return msg
		}
		return fmt.Sprintf("unknown key %q", key)
	case "type":
		if key := pointerLeaf(ve.InstanceLocation); key != "" {
			if want, ok := strings.CutPrefix(ve.Message, "value is not of type "); ok {
				msg := fmt.Sprintf("%q must be of type %s", key, want)
				if hint := schemaHint(strings.TrimSuffix(ve.KeywordLocation, "/type")); hint != "" {
					msg += "; " + hint
				}
				return msg
			}
		}
	case "pattern":
		// Every patterned key in rotini's schemas declares `examples`; quote them instead of
		// the regex.
		node := strings.TrimSuffix(ve.KeywordLocation, "/pattern")
		if ex := schemaExamples(node); len(ex) > 0 {
			msg := fmt.Sprintf("%s must look like %s", patternSubject(ve.InstanceLocation), quotedOrList(ex))
			if hint := schemaHint(node); hint != "" {
				msg += "; " + hint
			}
			return msg
		}
	case "anyOf":
		// A choice between forms of one value says what the forms are in its `x-hint`.
		if hint := schemaHint(strings.TrimSuffix(ve.KeywordLocation, "/anyOf")); hint != "" {
			return patternSubject(ve.InstanceLocation) + " " + hint
		}
		// Otherwise, in rotini's schemas an anyOf is a choice between required keys, so the
		// failed branches' causes name the choices.
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
// validator does not report, such as `examples` and `x-hint`.
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

// schemaHint returns the rotini-specific `x-hint` of the schema node at a keyword location,
// or "".
func schemaHint(location string) string {
	if m, ok := schemaNode(location).(map[string]any); ok {
		if h, ok := m["x-hint"].(string); ok {
			return h
		}
	}
	return ""
}

// schemaExamples returns the `examples` of the schema node at a keyword location
// ("#/definitions/BaseSchema/properties/$ref"), looked up in either embedded schema.
func schemaExamples(location string) []string {
	m, ok := schemaNode(location).(map[string]any)
	if !ok {
		return nil
	}
	raw, _ := m["examples"].([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// schemaNode resolves a keyword location in whichever embedded schema has it.
func schemaNode(location string) any {
	path := strings.Split(strings.TrimPrefix(location, "#/"), "/")
	for _, doc := range schemaDocuments() {
		node := doc
		for _, seg := range path {
			seg = unescapePointer(seg)
			switch n := node.(type) {
			case map[string]any:
				node = n[seg]
			case []any:
				i, err := strconv.Atoi(seg)
				if err != nil || i < 0 || i >= len(n) {
					node = nil
				} else {
					node = n[i]
				}
			default:
				node = nil
			}
		}
		if node != nil {
			return node
		}
	}
	return nil
}

// pointerLeaf returns the unescaped last segment of a JSON pointer ("/command/summry" ->
// "summry"), or "" for the root pointer.
func pointerLeaf(pointer string) string {
	i := strings.LastIndex(pointer, "/")
	if i < 0 || i == len(pointer)-1 {
		return ""
	}
	return unescapePointer(pointer[i+1:])
}

// definitionNoun returns the English noun for the definition a keyword location falls in
// ("#/definitions/Command/anyOf" -> "a command"), or "" when it names none.
func definitionNoun(keywordLocation string) string {
	name := definitionName(keywordLocation)
	if name == "" {
		return ""
	}
	noun := splitCamel(name)
	if strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

// definitionName returns the schema definition a keyword location falls in
// ("#/definitions/FlagInput/additionalProperties" -> "FlagInput"), or "" when it names none.
func definitionName(keywordLocation string) string {
	segs := strings.Split(strings.TrimPrefix(keywordLocation, "#/"), "/")
	for i, seg := range segs {
		if (seg == "definitions" || seg == "$defs") && i+1 < len(segs) && segs[i+1] != "" {
			return segs[i+1]
		}
	}
	return ""
}

// splitCamel turns a PascalCase schema definition name into lowercase words:
// "PluginSpec" -> "plugin spec".
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

// requiredChoices returns the deduplicated property each failed anyOf branch was missing, in
// branch order, or nil if any branch failed for another reason.
func requiredChoices(causes []jsonschema.ValidationError) []string {
	var keys []string
	for i := range causes {
		c := &causes[i]
		if c.Keyword != "required" {
			return nil
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

// Schema blocks cannot be closed with additionalProperties:false in the JSON Schema itself:
// Schema and InputSchema inherit BaseSchema through allOf, and Draft 7 cannot combine allOf
// with additionalProperties:false. schemaBlockProblems enforces closed keys in the validate
// stage instead.

// schemaBlockKeySets are the keys each kind of schema block may carry, derived from the
// embedded spec schema (BaseSchema's properties plus each definition's allOf additions).
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

// jsonSchemaOnlyKeywords are JSON Schema keywords rotini's schema blocks do not implement.
// An unknown key in this set gets a message saying so rather than a plain typo message.
var jsonSchemaOnlyKeywords = map[string]bool{
	"const": true, "format": true, "contains": true, "minContains": true,
	"maxContains": true, "additionalProperties": true, "patternProperties": true,
	"propertyNames": true, "minProperties": true, "maxProperties": true, "dependencies": true,
	"dependentRequired": true, "dependentSchemas": true, "if": true, "then": true, "else": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true, "additionalItems": true,
	"unevaluatedItems": true, "unevaluatedProperties": true, "$id": true, "$defs": true,
	"definitions": true, "title": true, "description": true, "examples": true, "$comment": true,
	"readOnly": true, "writeOnly": true, "deprecated": true, "contentMediaType": true,
	"contentEncoding": true, "default": true,
}

// schemaBlockProblems reports every key in a spec's schema blocks that the block's kind does
// not define, positioned by JSON pointer. It visits input schemas on each channel, output and
// exit-status output, named schemas, and config file schemas, recursing through properties
// and items.
func schemaBlockProblems(instance []byte) []error {
	sets, err := loadSchemaBlockKeys()
	if err != nil {
		return []error{err}
	}
	var doc map[string]any
	if json.Unmarshal(instance, &doc) != nil {
		return nil // the schema validator reports a malformed document
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
var inputChannels = []struct{ key, noun, channel string }{
	{"flags", "a flag schema", "flag"}, {"arguments", "an argument schema", "argument"},
	{"env", "an env schema", "env"}, {"config", "a config schema", "config"},
}

// command checks every schema block one command owns, then its sub-commands.
func (w *schemaBlockWalker) command(node any, ptr string) {
	c, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, ch := range inputChannels {
		w.entrySchemas(c[ch.key], true, ptr+"/"+ch.key, ch.noun, ch.channel)
	}
	w.entrySchemas(c["config_files"], false, ptr+"/config_files", "a config file schema", "")
	if stdin, ok := c["stdin"].(map[string]any); ok {
		if s, ok := stdin["schema"]; ok {
			w.block(s, true, ptr+"/stdin/schema", "the stdin schema", "stdin")
		}
	}
	if out, ok := c["output"]; ok {
		w.block(out, false, ptr+"/output", "the output schema", "")
	}
	if statuses, ok := c["exit_status"].([]any); ok {
		for i, s := range statuses {
			if e, ok := s.(map[string]any); ok {
				if out, ok := e["output"]; ok {
					w.block(out, false, fmt.Sprintf("%s/exit_status/%d/output", ptr, i), "an exit status output schema", "")
				}
			}
		}
	}
	if named, ok := c["schemas"].(map[string]any); ok {
		for _, n := range slices.Sorted(maps.Keys(named)) {
			w.block(named[n], false, ptr+"/schemas/"+escapePointer(n), "a named schema", "")
		}
	}
	subs, _ := c["commands"].([]any)
	for i, sub := range subs {
		w.command(sub, fmt.Sprintf("%s/commands/%d", ptr, i))
	}
}

// entrySchemas checks the `schema` of each entry in a list such as flags or config_files.
func (w *schemaBlockWalker) entrySchemas(list any, input bool, ptr, noun, channel string) {
	entries, _ := list.([]any)
	for i, entry := range entries {
		if e, ok := entry.(map[string]any); ok {
			if s, ok := e["schema"]; ok {
				w.block(s, input, fmt.Sprintf("%s/%d/schema", ptr, i), noun, channel)
			}
		}
	}
}

// block checks one schema block's keys, then recurses into its properties and items, which
// are always object schemas regardless of the containing block's kind. channel names the input
// channel whose entry holds the block ("flag", "stdin"), or "" for any other block.
func (w *schemaBlockWalker) block(node any, input bool, ptr, noun, channel string) {
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
			w.problems = append(w.problems, &problem{kind: "spec", loc: ptr + "/" + escapePointer(k), msg: unknownSchemaKey(k, noun, channel)})
		}
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for _, k := range slices.Sorted(maps.Keys(props)) {
			w.block(props[k], false, ptr+"/properties/"+escapePointer(k), "a property schema", "")
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		w.block(items, false, ptr+"/items", "an items schema", "")
	}
}

// unknownSchemaKey renders the unknown-key message. A key that belongs on the input entry
// beside the block says so; otherwise the message notes when the key is an unimplemented JSON
// Schema keyword.
func unknownSchemaKey(key, noun, channel string) string {
	msg := fmt.Sprintf("unknown key %q in %s", key, noun)
	if hint := entryPlacementHint(channel, key); hint != "" {
		return msg + "; " + hint
	}
	if jsonSchemaOnlyKeywords[key] {
		msg += fmt.Sprintf("; %q is a JSON Schema keyword rotini's schema blocks do not implement, so it would have done nothing", key)
	}
	return msg
}

// escapePointer escapes one JSON pointer segment (RFC 6901).
func escapePointer(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), "/", "~1")
}

// unescapePointer undoes escapePointer: "~1" first, then "~0", the order RFC 6901 requires.
func unescapePointer(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
}
