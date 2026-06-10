package internal

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// This file owns the `rotini validate` operation end-to-end: the session/file-level
// validation that the Processor drives, plus the machinery (schema validation, the
// $schema↔version guard, and the rotini-specific rules the JSON Schema can't express).

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

// ─── session + file validation ────────────────────────────────────────────────

// validate runs the validator phase over the loaded spec and conf (call load first):
// each file validates itself. Problems are aggregated via errors.Join, or — in fast
// mode — the first is returned. It returns nil when both are valid.
func (s *session) validate() error {
	fast := s.failFast()

	problems := s.spec.validate()
	if fast && len(problems) > 0 {
		return problems[0]
	}

	problems = append(problems, s.conf.validate()...)
	if fast && len(problems) > 0 {
		return problems[0]
	}
	return errors.Join(problems...)
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
// the rotini-specific rules and enforces the $schema↔version guard. It returns every
// problem found, empty when the spec is valid.
func (l *specLoader) validate() []error {
	if problems := validateDocument(l.path, "spec", l.schema); len(problems) > 0 {
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(l.spec)...)
	}
	if err := checkSchemaVersion("spec", l.spec.Schema, l.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// validate schema-validates the conf against its compiled schema on the raw JSON
// instance when one was resolved, then enforces the $schema↔version guard. A default
// conf (no file) has nothing to validate. It returns every problem found.
func (l *confLoader) validate() []error {
	if l.path == "" {
		return nil
	}
	if problems := validateDocument(l.path, "conf", l.schema); len(problems) > 0 {
		return problems
	}
	if err := checkSchemaVersion("conf", l.conf.Schema, l.version); err != nil {
		return []error{err}
	}
	return nil
}

// ─── schema validation + the $schema↔version guard ─────────────────────────────

// problem is a single validation failure: the location of the offending value within
// the document and a human-readable message, tagged by document kind ("spec"/"conf").
type problem struct {
	kind string
	loc  string
	msg  string
}

func (e *problem) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// validateDocument reads the document at path and validates its raw JSON instance (so
// schema rules like additionalProperties:false see unknown fields) against the given
// compiled schema, returning one error per problem: a read/convert failure, or one
// [*problem] per schema violation. It returns nil when the document is valid.
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

// rotiniSchemaURLRe matches the recognized rotini `$schema` URL form and captures the
// X.Y.Z version segment. A URL that doesn't match (absent, a branch ref, a different
// host) yields no capture, and checkSchemaVersion skips it.
var rotiniSchemaURLRe = regexp.MustCompile(`^https://raw\.githubusercontent\.com/go-rotini/rotini/refs/tags/([0-9]+\.[0-9]+\.[0-9]+)/schema-(?:spec|conf)\.json$`)

// checkSchemaVersion enforces that a document's `$schema` targets the same rotini
// release as the running binary. version is the binary's bound version string
// ("vX.Y.Z" or "v0.0.0"); its leading "v" is stripped to the "X.Y.Z" segment compared
// against the document's `$schema` version. The check is skipped when the version is
// empty/unknown, or when the document's `$schema` is absent or not the recognized
// rotini refs/tags/<VER> form. A present, recognized, mismatched `$schema` is an error.
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

// ─── the rotini-specific rules (what the JSON Schema can't express) ─────────────

// specLints is the ordered set of spec rules run after the spec is schema-valid.
// Adding a rule is a one-line append here; each stays a pure func(*Spec) []error for
// isolated testing. The order is observable (collect mode joins problems in order), so
// keep it stable.
var specLints = []func(*Spec) []error{
	lintImportConsistency,
	lintLocalTimeout,
	lintFlagGroups,
	lintFlagDependencies,
	lintDuplicateFlagIdentifiers,
	lintSchemaRefs,
}

// walkCommands visits every command in the spec depth-first (pre-order), passing a
// display path — "root/child/grandchild", using a child's $ref segment when it has
// no name (and "(root)" for an unnamed root). The rules call this instead of each
// re-defining the same recursive walk.
func walkCommands(spec *Spec, visit func(c *Command, path string)) {
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		visit(c, path)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, path+"/"+seg)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, name)
}

// flagNames returns the set of a command's declared flag names plus an ordered slice
// of them (for closestName "did you mean?" suggestions). Shared by the flag_groups
// and flag_dependencies rules.
func flagNames(c *Command) (known map[string]bool, ordered []string) {
	known = map[string]bool{}
	if c.Inputs == nil {
		return known, nil
	}
	ordered = make([]string, 0, len(c.Inputs.Flags))
	for _, f := range c.Inputs.Flags {
		known[f.Name] = true
		ordered = append(ordered, f.Name)
	}
	return known, ordered
}

// eachInputSchema visits each declared input schema of a command across all
// channels (flag, argument, env, config, stdin), passing the channel label and the
// input's logical name ("" for stdin). It is the single place the schema-walking
// rules enumerate a command's input channels.
func eachInputSchema(in *Inputs, visit func(channel, name string, schema *InputSchema)) {
	if in == nil {
		return
	}
	for i := range in.Flags {
		visit("flag", in.Flags[i].Name, in.Flags[i].Schema)
	}
	for i := range in.Arguments {
		visit("argument", in.Arguments[i].Name, in.Arguments[i].Schema)
	}
	for i := range in.Env {
		visit("env", in.Env[i].Name, in.Env[i].Schema)
	}
	for i := range in.Config {
		visit("config", in.Config[i].Name, in.Config[i].Schema)
	}
	if in.Stdin != nil {
		visit("stdin", "", in.Stdin.Schema)
	}
}

// didYouMean appends a "; did you mean %q?" suffix to msg when one of candidates is
// a near-match (edit distance < 3) for name, else returns msg unchanged. Shared by
// the rules that suggest a fix for a typo'd flag or schema name.
func didYouMean(msg, name string, candidates []string) string {
	if s := closestName(name, candidates); s != "" {
		return msg + fmt.Sprintf("; did you mean %q?", s)
	}
	return msg
}

// lintImportConsistency reports any `type:` declared with two or more different
// `import:` values across the spec — the same type with two backing packages is
// always a bug. (A wrong-but-consistent import is left to `go build`; this catches
// the contradictory case at validate time.) One problem per offending type, sorted.
func lintImportConsistency(spec *Spec) []error {
	byType := map[string]map[string]bool{}
	record := func(typ, imp string) {
		typ, imp = strings.TrimSpace(typ), strings.TrimSpace(imp)
		if typ == "" || imp == "" {
			return
		}
		if byType[typ] == nil {
			byType[typ] = map[string]bool{}
		}
		byType[typ][imp] = true
	}
	walkCommands(spec, func(c *Command, _ string) {
		eachInputSchema(c.Inputs, func(_, _ string, s *InputSchema) {
			if s != nil {
				walkSchemaImports(s.BaseSchema, record)
			}
		})
		if c.Output != nil {
			walkSchemaImports(c.Output.BaseSchema, record)
		}
	})
	for name := range spec.Schemas {
		s := spec.Schemas[name]
		walkSchemaImports(s.BaseSchema, record)
	}

	var problems []error
	for typ, imps := range byType {
		if len(imps) < 2 {
			continue
		}
		list := make([]string, 0, len(imps))
		for imp := range imps {
			list = append(list, imp)
		}
		sort.Strings(list)
		problems = append(problems, &problem{
			kind: "spec",
			loc:  "type " + typ,
			msg:  "declared with conflicting imports (" + strings.Join(list, ", ") + "); a type must have one backing package",
		})
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return problems
}

// lintLocalTimeout rejects a `timeout` declared on any command. A timeout is a
// remote-only, host-side bound on a dispatched binary (set per remote_commands entry,
// honored by the remote runtime); on a local command it is never honored, so accepting
// it would be a silent lie. One problem per offending command, in tree order. The
// remote_commands[].timeout is a separate field and is left untouched.
func lintLocalTimeout(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if strings.TrimSpace(c.Timeout) != "" {
			problems = append(problems, &problem{
				kind: "spec",
				loc:  "command " + path,
				msg:  "timeout is not supported on a local command — it is a remote-only, host-side bound with no effect here; set it on a remote_commands entry's timeout instead",
			})
		}
	})
	return problems
}

// lintFlagGroups checks that every flag_groups entry references flags that actually
// exist on the same command (a typo'd flag name would otherwise silently never match
// at runtime). One problem per bad reference, in tree order.
func lintFlagGroups(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil || len(c.Inputs.FlagGroups) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for _, g := range c.Inputs.FlagGroups {
			for _, name := range g.Flags {
				if !known[name] {
					msg := fmt.Sprintf("flag_groups (%s) references unknown flag %q — it has no matching entry in this command's flags", g.Kind, name)
					problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, name, ordered)})
				}
			}
		}
	})
	return problems
}

// lintFlagDependencies rejects a flag_dependencies entry whose When or Requires
// references a flag the command doesn't declare — the conditional could never fire (or
// could never be satisfied), masking a typo.
func lintFlagDependencies(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil || len(c.Inputs.FlagDependencies) == 0 {
			return
		}
		known, ordered := flagNames(c)
		report := func(name string) {
			msg := fmt.Sprintf("flag_dependencies references unknown flag %q — it has no matching entry in this command's flags", name)
			problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, name, ordered)})
		}
		for _, dep := range c.Inputs.FlagDependencies {
			if !known[dep.When] {
				report(dep.When)
			}
			for _, name := range dep.Requires {
				if !known[name] {
					report(name)
				}
			}
		}
	})
	return problems
}

// lintDuplicateFlagIdentifiers rejects a command that declares the same flag identifier
// twice — a collision the parser would resolve silently (last/first wins), masking the
// author's intent. Each flag's effective identifiers are its declared ones, or the
// auto-derived "--<name>" when it declares none, so both explicit ("-o" on two flags)
// and derived (two flags whose names both yield "--out") collisions are caught.
func lintDuplicateFlagIdentifiers(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil {
			return
		}
		claimedBy := map[string]string{} // identifier -> the flag name that first claimed it
		for _, f := range c.Inputs.Flags {
			ids := f.Identifiers
			if len(ids) == 0 {
				ids = []string{"--" + f.Name}
			}
			for _, id := range ids {
				if prev, dup := claimedBy[id]; dup {
					problems = append(problems, &problem{
						kind: "spec",
						loc:  "command " + path,
						msg:  fmt.Sprintf("flag identifier %q is declared by both %q and %q", id, prev, f.Name),
					})
					continue
				}
				claimedBy[id] = f.Name
			}
		}
	})
	return problems
}

// lintSchemaRefs rejects an intra-document "$ref": "#/schemas/X" that points to a schema
// the document doesn't declare — which would otherwise become an "undefined type X"
// compile error in the generated code instead of a clear spec error. It walks every
// place a ref can appear (each command's flag/argument/env/config/stdin input schemas and
// output schema, plus the document-level schemas themselves, recursing into object
// properties and array items) and offers the closest declared schema name as a suggestion.
func lintSchemaRefs(spec *Spec) []error {
	declared := map[string]bool{}
	names := make([]string, 0, len(spec.Schemas))
	for name := range spec.Schemas {
		declared[name] = true
		names = append(names, name)
	}
	var problems []error
	reported := map[string]bool{}
	checkAt := func(loc string) func(BaseSchema) {
		return func(b BaseSchema) {
			name := refTypeName(b.Ref)
			if name == "" || declared[name] {
				return
			}
			if key := name + "\x00" + loc; reported[key] {
				return
			} else {
				reported[key] = true
			}
			msg := fmt.Sprintf("$ref %q points to an undeclared schema (no %q under the document-level \"schemas\")", b.Ref, name)
			problems = append(problems, &problem{kind: "spec", loc: loc, msg: didYouMean(msg, name, names)})
		}
	}
	walkCommands(spec, func(c *Command, path string) {
		eachInputSchema(c.Inputs, func(channel, name string, s *InputSchema) {
			if s == nil {
				return
			}
			loc := "command " + path + " " + channel
			if name != "" {
				loc += " " + name
			}
			walkSchemaRefs(s.BaseSchema, checkAt(loc))
		})
		if c.Output != nil {
			walkSchemaRefs(c.Output.BaseSchema, checkAt("command "+path+" output"))
		}
	})
	for name := range spec.Schemas {
		s := spec.Schemas[name]
		walkSchemaRefs(s.BaseSchema, checkAt("schema "+name))
	}
	return problems
}

// walkSchemaRefs invokes visit for a schema and recurses into its object properties and
// array items, so a "$ref" at any nesting depth is seen.
func walkSchemaRefs(b BaseSchema, visit func(BaseSchema)) {
	visit(b)
	for _, p := range b.Properties {
		walkSchemaRefs(p.BaseSchema, visit)
	}
	if b.Items != nil {
		walkSchemaRefs(b.Items.BaseSchema, visit)
	}
}

// walkSchemaImports records (type, import) for a schema and recurses into its object
// properties and array items.
func walkSchemaImports(b BaseSchema, record func(typ, imp string)) {
	record(b.Type, b.Import)
	for _, p := range b.Properties {
		walkSchemaImports(p.BaseSchema, record)
	}
	if b.Items != nil {
		walkSchemaImports(b.Items.BaseSchema, record)
	}
}

// levenshtein is the edit distance between a and b (Wagner–Fischer).
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// closestName returns the candidate within edit distance 2 of target (the nearest typo
// fix), or "" when none is close enough.
func closestName(target string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(target, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}
