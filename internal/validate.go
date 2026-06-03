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
			for _, f := range c.Inputs.Flags {
				known[f.Name] = true
			}
			for _, g := range c.Inputs.FlagGroups {
				for _, name := range g.Flags {
					if !known[name] {
						problems = append(problems, &violationError{
							kind: "spec",
							loc:  "command " + path,
							msg:  fmt.Sprintf("flag_groups (%s) references unknown flag %q — it has no matching entry in this command's flags", g.Kind, name),
						})
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
