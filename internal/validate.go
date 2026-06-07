package internal

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini"
)

// errSpecPathRequired is reported by [Validate] when no spec-file path is
// supplied.
var errSpecPathRequired = errors.New("spec file path is required")

// The embedded rotini JSON Schemas are immutable, so each is compiled at
// most once per process and the result cached.
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	errSpecSchema  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	errConfSchema  error
)

// violationError is a single schema-validation failure: the location of the
// offending value within the document and a human-readable message, tagged
// by document kind ("spec" or "conf").
type violationError struct {
	kind string
	loc  string
	msg  string
}

func (e *violationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// loadSpecSchema compiles the embedded spec JSON Schema once and returns
// the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaSpec)
		if err != nil {
			errSpecSchema = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		specSchema = s
	})
	return specSchema, errSpecSchema
}

// loadConfSchema compiles the embedded conf JSON Schema once and returns
// the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaConf)
		if err != nil {
			errConfSchema = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		confSchema = s
	})
	return confSchema, errConfSchema
}

// Validate checks a rotini spec file, and optionally a conf file, against
// the embedded rotini JSON Schemas.
//
// specPath is the spec-file command argument and is required. confPath is
// the value of the -c/--config flag; pass "" when no conf file was given.
// The rotini handler parses those flags and passes their values here.
//
// Every problem found — a missing or unreadable file, a format-conversion
// ValidateFn is the signature of [Validate]. A command handler can bind it under a registry
// key and fetch it as an injectable service, so tests substitute a double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath, failMode string) error

// failure, or an individual schema violation — is aggregated and returned
// as a single error via [errors.Join]; the calling handler unwraps it for
// display. Validate returns nil when the spec (and conf, if given) are
// valid.
func Validate(specPath, confPath, failMode string) error {
	fast := resolveFailMode(failMode) == "fast"

	var problems []error
	if specPath == "" {
		problems = append(problems, errSpecPathRequired)
	} else {
		specProblems := validateDocument(specPath, "spec", loadSpecSchema)
		problems = append(problems, specProblems...)
		// The import-consistency lint runs on the decoded spec, so only attempt it
		// once the document is schema-valid (otherwise the decode is meaningless).
		if len(specProblems) == 0 {
			if spec, err := ReadSpec(specPath); err == nil {
				problems = append(problems, lintImportConsistency(spec)...)
				problems = append(problems, lintLocalTimeout(spec)...)
				problems = append(problems, lintFlagGroups(spec)...)
				problems = append(problems, lintFlagDependencies(spec)...)
				problems = append(problems, lintDuplicateFlagIdentifiers(spec)...)
				problems = append(problems, lintSchemaRefs(spec)...)
			}
		}
	}
	if fast && len(problems) > 0 {
		return problems[0]
	}
	if confPath != "" {
		problems = append(problems, validateDocument(confPath, "conf", loadConfSchema)...)
	}
	if fast && len(problems) > 0 {
		return problems[0]
	}

	return errors.Join(problems...)
}

// resolveFailMode resolves the validate failure-reporting mode: an explicit value
// (the --fail flag) wins; otherwise the module-root conf's validate.fail is used,
// defaulting to "collect". Only "fast" enables fast mode; anything else collects.
func resolveFailMode(failMode string) string {
	if failMode != "" {
		return failMode
	}
	root, _, err := findModule()
	if err != nil {
		return "collect"
	}
	confPath, err := discoverFile(root, ".rotini.conf.")
	if err != nil {
		return "collect"
	}
	conf, err := ReadConf(confPath)
	if err != nil || conf.Validate == nil {
		return "collect"
	}
	return conf.Validate.Fail
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
	feedInput := func(s *InputSchema) {
		if s != nil {
			walkSchemaImports(s.BaseSchema, record)
		}
	}
	var walkCmd func(c *Command)
	walkCmd = func(c *Command) {
		if c.Inputs != nil {
			for i := range c.Inputs.Flags {
				feedInput(c.Inputs.Flags[i].Schema)
			}
			for i := range c.Inputs.Arguments {
				feedInput(c.Inputs.Arguments[i].Schema)
			}
			for i := range c.Inputs.Env {
				feedInput(c.Inputs.Env[i].Schema)
			}
			for i := range c.Inputs.Config {
				feedInput(c.Inputs.Config[i].Schema)
			}
			if c.Inputs.Stdin != nil {
				feedInput(c.Inputs.Stdin.Schema)
			}
		}
		if c.Output != nil {
			walkSchemaImports(c.Output.BaseSchema, record)
		}
		for i := range c.Commands {
			walkCmd(&c.Commands[i])
		}
	}
	walkCmd(&spec.Command)
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
		problems = append(problems, &violationError{
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
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		if strings.TrimSpace(c.Timeout) != "" {
			problems = append(problems, &violationError{
				kind: "spec",
				loc:  "command " + path,
				msg:  "timeout is not supported on a local command — it is a remote-only, host-side bound with no effect here; set it on a remote_commands entry's timeout instead",
			})
		}
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
	return problems
}

// lintFlagGroups checks that every flag_groups entry references flags that actually
// exist on the same command (a typo'd flag name would otherwise silently never match
// at runtime). One problem per bad reference, in tree order.
func lintFlagGroups(spec *Spec) []error {
	var problems []error
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		if c.Inputs != nil && len(c.Inputs.FlagGroups) > 0 {
			known := map[string]bool{}
			flagNames := make([]string, 0, len(c.Inputs.Flags))
			for _, f := range c.Inputs.Flags {
				known[f.Name] = true
				flagNames = append(flagNames, f.Name)
			}
			for _, g := range c.Inputs.FlagGroups {
				for _, name := range g.Flags {
					if !known[name] {
						msg := fmt.Sprintf("flag_groups (%s) references unknown flag %q — it has no matching entry in this command's flags", g.Kind, name)
						if s := closestName(name, flagNames); s != "" {
							msg += fmt.Sprintf("; did you mean %q?", s)
						}
						problems = append(problems, &violationError{kind: "spec", loc: "command " + path, msg: msg})
					}
				}
			}
		}
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
	return problems
}

// lintFlagDependencies rejects a flag_dependencies entry whose When or Requires
// references a flag the command doesn't declare — the conditional could never fire (or
// could never be satisfied), masking a typo.
func lintFlagDependencies(spec *Spec) []error {
	var problems []error
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		if c.Inputs != nil && len(c.Inputs.FlagDependencies) > 0 {
			known := map[string]bool{}
			flagNames := make([]string, 0, len(c.Inputs.Flags))
			for _, f := range c.Inputs.Flags {
				known[f.Name] = true
				flagNames = append(flagNames, f.Name)
			}
			report := func(name string) {
				msg := fmt.Sprintf("flag_dependencies references unknown flag %q — it has no matching entry in this command's flags", name)
				if s := closestName(name, flagNames); s != "" {
					msg += fmt.Sprintf("; did you mean %q?", s)
				}
				problems = append(problems, &violationError{kind: "spec", loc: "command " + path, msg: msg})
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
		}
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
	return problems
}

// lintDuplicateFlagIdentifiers rejects a command that declares the same flag identifier
// twice — a collision the parser would resolve silently (last/first wins), masking the
// author's intent. Each flag's effective identifiers are its declared ones, or the
// auto-derived "--<name>" when it declares none, so both explicit ("-o" on two flags)
// and derived (two flags whose names both yield "--out") collisions are caught.
func lintDuplicateFlagIdentifiers(spec *Spec) []error {
	var problems []error
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		if c.Inputs != nil {
			claimedBy := map[string]string{} // identifier -> the flag name that first claimed it
			for _, f := range c.Inputs.Flags {
				ids := f.Identifiers
				if len(ids) == 0 {
					ids = []string{"--" + f.Name}
				}
				for _, id := range ids {
					if prev, dup := claimedBy[id]; dup {
						problems = append(problems, &violationError{
							kind: "spec",
							loc:  "command " + path,
							msg:  fmt.Sprintf("flag identifier %q is declared by both %q and %q", id, prev, f.Name),
						})
						continue
					}
					claimedBy[id] = f.Name
				}
			}
		}
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
			if s := closestName(name, names); s != "" {
				msg += fmt.Sprintf("; did you mean %q?", s)
			}
			problems = append(problems, &violationError{kind: "spec", loc: loc, msg: msg})
		}
	}
	feed := func(s *InputSchema, loc string) {
		if s != nil {
			walkSchemaRefs(s.BaseSchema, checkAt(loc))
		}
	}
	var walkCmd func(c *Command, path string)
	walkCmd = func(c *Command, path string) {
		if c.Inputs != nil {
			for i := range c.Inputs.Flags {
				feed(c.Inputs.Flags[i].Schema, "command "+path+" flag "+c.Inputs.Flags[i].Name)
			}
			for i := range c.Inputs.Arguments {
				feed(c.Inputs.Arguments[i].Schema, "command "+path+" argument "+c.Inputs.Arguments[i].Name)
			}
			for i := range c.Inputs.Env {
				feed(c.Inputs.Env[i].Schema, "command "+path+" env "+c.Inputs.Env[i].Name)
			}
			for i := range c.Inputs.Config {
				feed(c.Inputs.Config[i].Schema, "command "+path+" config "+c.Inputs.Config[i].Name)
			}
			if c.Inputs.Stdin != nil {
				feed(c.Inputs.Stdin.Schema, "command "+path+" stdin")
			}
		}
		if c.Output != nil {
			walkSchemaRefs(c.Output.BaseSchema, checkAt("command "+path+" output"))
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walkCmd(child, path+"/"+seg)
		}
	}
	rootName := spec.Command.Name
	if rootName == "" {
		rootName = "(root)"
	}
	walkCmd(&spec.Command, rootName)
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

// walkSchemaImports records (type, import) for a schema and recurses into its
// object properties and array items.
func walkSchemaImports(b BaseSchema, record func(typ, imp string)) {
	record(b.Type, b.Import)
	for _, p := range b.Properties {
		walkSchemaImports(p.BaseSchema, record)
	}
	if b.Items != nil {
		walkSchemaImports(b.Items.BaseSchema, record)
	}
}

// validateDocument reads the document at path, compiles its schema, and
// returns one error per problem: a read/convert failure, a schema-compile
// failure, or one [*violationError] per schema violation. It returns nil
// when the document is valid.
func validateDocument(path, kind string, loadSchema func() (*jsonschema.Schema, error)) []error {
	instance, err := toJSON(path)
	if err != nil {
		return []error{fmt.Errorf("%s file: %w", kind, err)}
	}
	schema, err := loadSchema()
	if err != nil {
		return []error{err}
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
		problems = append(problems, &violationError{kind: kind, loc: loc, msg: ve.Message})
	}
	return problems
}
