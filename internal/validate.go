package internal

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini/schemas"
)

// The .rotini.spec / .rotini.conf JSON Schemas are embedded in and
// exported by the schemas package ([schemas.RotiniSchemaSpec] /
// [schemas.RotiniSchemaConf]); we compile those bytes here so
// `go tool rotini validate` works against any spec file without
// external dependencies.

// Compiled regex patterns mirroring the schema constraints. They are
// duplicated in code so the semantic checks (which operate after JSON
// parsing) can produce useful per-field diagnostics — the JSON Schema
// engine produces less actionable messages when a single regex fails.
var (
	reIdentifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
	reEventKey   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reFlagID     = regexp.MustCompile(`^-{1,2}[a-zA-Z][a-zA-Z0-9_-]*$`)
	reEnvVar     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	reExported   = regexp.MustCompile(`^[A-Z][a-zA-Z0-9_]*$`)
	reConfigKey  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)
)

// validConfigFormats is the set of allowed values for [ConfigSpec.Format].
var validConfigFormats = map[string]bool{"json": true, "yaml": true, "toml": true}

// compiledSpecSchema is the lazily-compiled JSON Schema instance for
// the spec schema. We compile once on first use and reuse for every
// subsequent Validate call. sync.Once gates the compile so concurrent
// Validate calls don't race on initialization.
var (
	specSchemaOnce       sync.Once
	specSchemaVal        *jsonschema.Schema
	errSpecSchemaCompile error
)

// compiledConfSchema is the lazily-compiled JSON Schema instance for
// the conf schema.
var (
	confSchemaOnce       sync.Once
	confSchemaVal        *jsonschema.Schema
	errConfSchemaCompile error
)

// Validate runs both the JSON Schema validation and the semantic checks
// against s. It returns nil when the spec is valid, otherwise a
// [*SpecError] aggregating every problem found.
//
// JSON Schema catches the structural rules (required fields, types,
// regex patterns). Semantic checks catch the cross-cutting rules the
// schema cannot express:
//
//   - duplicate command names within siblings (including aliases)
//   - duplicate flag names within a command
//   - duplicate flag identifiers within a command
//   - duplicate argument names within a command
//   - variadic argument must be the last argument
//   - $ref targets resolve against declared schemas
//   - durations are valid Go duration strings
//   - config-file references in inputs.files[].schema.file resolve to
//     a declared files[] entry
func Validate(s *Spec) error {
	if s == nil {
		errs := &SpecError{}
		errs.addf("", "spec is nil")
		return errs.nonEmpty()
	}

	errs := &SpecError{}
	if err := validateSpecAgainstSchema(s, errs); err != nil {
		return err
	}
	validateSpecSemantics(s, errs)
	return errs.nonEmpty()
}

// ValidateConf runs the JSON Schema validation against c. Returns nil on
// success.
func ValidateConf(c *Conf) error {
	if c == nil {
		// nil conf is allowed — Run() will use defaults.
		return nil
	}
	errs := &SpecError{}
	if err := validateConfAgainstSchema(c, errs); err != nil {
		return err
	}
	return errs.nonEmpty()
}

// validateSpecAgainstSchema marshals s to JSON and runs the embedded
// JSON Schema over it. Schema-level violations are appended to errs.
// Engine-level failures (compile error, marshal error) are returned
// directly so the caller can distinguish "spec invalid" from "validator
// is itself broken".
func validateSpecAgainstSchema(s *Spec, errs *SpecError) error {
	sch, err := loadSpecSchema()
	if err != nil {
		return fmt.Errorf("internal: load spec schema: %w", err)
	}
	instance, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("internal: marshal spec for validation: %w", err)
	}
	result, err := sch.Validate(instance)
	if err != nil {
		return fmt.Errorf("internal: run spec schema validation: %w", err)
	}
	collectSchemaIssues(result, errs)
	return nil
}

// validateConfAgainstSchema is the conf-file analog of
// [validateSpecAgainstSchema].
func validateConfAgainstSchema(c *Conf, errs *SpecError) error {
	sch, err := loadConfSchema()
	if err != nil {
		return fmt.Errorf("internal: load conf schema: %w", err)
	}
	instance, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("internal: marshal conf for validation: %w", err)
	}
	result, err := sch.Validate(instance)
	if err != nil {
		return fmt.Errorf("internal: run conf schema validation: %w", err)
	}
	collectSchemaIssues(result, errs)
	return nil
}

// loadSpecSchema returns the compiled spec schema, lazily compiling it on
// first call. Subsequent calls reuse the cached value. sync.Once
// guarantees safety under concurrent Validate calls.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() {
		sch, err := jsonschema.Compile(schemas.RotiniSchemaSpec)
		if err != nil {
			errSpecSchemaCompile = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		specSchemaVal = sch
	})
	return specSchemaVal, errSpecSchemaCompile
}

// loadConfSchema returns the compiled conf schema, lazily compiling it on
// first call.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() {
		sch, err := jsonschema.Compile(schemas.RotiniSchemaConf)
		if err != nil {
			errConfSchemaCompile = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		confSchemaVal = sch
	})
	return confSchemaVal, errConfSchemaCompile
}

// collectSchemaIssues translates a jsonschema.Result into SpecIssues
// appended to errs. Pointer-style InstanceLocation values
// ("/commands/0/name") are converted to the dotted form
// ("commands[0].name") consistent with the semantic-check messages.
func collectSchemaIssues(result *jsonschema.Result, errs *SpecError) {
	if result == nil || result.Valid {
		return
	}
	for i := range result.Errors {
		ve := &result.Errors[i]
		errs.addKeywordf(jsonPointerToDotted(ve.InstanceLocation), "schema", "%s", ve.Message)
	}
}

// jsonPointerToDotted converts a JSON Pointer "/a/0/b" to dotted form
// "a[0].b". Numeric segments become bracketed indices; non-numeric
// segments are joined by dots.
func jsonPointerToDotted(ptr string) string {
	if ptr == "" || ptr == "/" {
		return ""
	}
	var b strings.Builder
	first := true
	for seg := range strings.SplitSeq(strings.TrimPrefix(ptr, "/"), "/") {
		if isNumericSegment(seg) {
			fmt.Fprintf(&b, "[%s]", seg)
		} else {
			if !first {
				b.WriteByte('.')
			}
			b.WriteString(seg)
		}
		first = false
	}
	return b.String()
}

// isNumericSegment reports whether seg consists solely of decimal digits.
func isNumericSegment(seg string) bool {
	if seg == "" {
		return false
	}
	for i := range seg {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	return true
}

// validateSpecSemantics runs the cross-cutting checks that JSON Schema
// can't express. Issues are appended to errs.
func validateSpecSemantics(s *Spec, errs *SpecError) {
	configFileNames := validateConfigSpecs(s.Files, errs)
	validateDuration(s.Timeout, "timeout", errs)

	if s.Inputs != nil {
		validateInputs(s.Inputs, "", configFileNames, errs)
	}

	validateCommands(s.Commands, s.RemoteCommands, "", configFileNames, errs)

	validateEventNames(s.Events, errs)
	validateMetadataEntries(s.Metadata, errs)
	validateRefs(s, errs)
}

// validateDuration appends an issue when s is a non-empty value that
// doesn't parse as a Go duration.
func validateDuration(s, location string, errs *SpecError) {
	if s == "" {
		return
	}
	if _, err := time.ParseDuration(s); err != nil {
		errs.addKeywordf(location, "duration", "%q is not a valid Go duration string (e.g., \"10s\", \"1m30s\"): %v", s, err)
	}
}

// validateConfigSpecs checks the files[] declarations and returns the
// set of names declared. Used downstream by [validateInputs] to verify
// inputs.files[].schema.file references resolve.
func validateConfigSpecs(specs []ConfigSpec, errs *SpecError) map[string]bool {
	names := make(map[string]bool, len(specs))
	for i, cs := range specs {
		path := fmt.Sprintf("files[%d]", i)
		switch {
		case cs.Name == "":
			errs.addKeywordf(path+".name", "required", "configuration file name is required")
		case names[cs.Name]:
			errs.addKeywordf(path+".name", "duplicate", "duplicate configuration file name %q", cs.Name)
		default:
			names[cs.Name] = true
		}
		if cs.Path == "" {
			errs.addKeywordf(path+".path", "required", "configuration file path is required")
		}
		if cs.Format != "" && !validConfigFormats[cs.Format] {
			errs.addKeywordf(path+".format", "enum", "%q is not valid; must be \"json\", \"yaml\", or \"toml\"", cs.Format)
		}
	}
	return names
}

// validateCommands walks every command level checking name patterns,
// alias uniqueness across siblings (the routing namespace), per-command
// inputs, and recursively into sub-commands.
func validateCommands(cmds []Command, remoteCmds []RemoteCommandSpec, parentPath string, configFileNames map[string]bool, errs *SpecError) {
	// All names and aliases at this level share a single routing
	// namespace. Track the first owner per token so duplicates can point
	// back to the original declaration.
	seen := make(map[string]string)

	claim := func(token, owner string) {
		if first, dup := seen[token]; dup {
			errs.addKeywordf(owner, "duplicate", "%q clashes with %s in the same command namespace", token, first)
		} else {
			seen[token] = owner
		}
	}

	for i := range cmds {
		cmd := &cmds[i]
		path := joinPath(parentPath, fmt.Sprintf("commands[%d]", i))

		switch {
		case cmd.Name == "":
			errs.addKeywordf(path+".name", "required", "command name is required")
		case !reIdentifier.MatchString(cmd.Name):
			errs.addKeywordf(path+".name", "pattern", "%q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", cmd.Name)
			fallthrough
		default:
			if cmd.Name != "" {
				claim(cmd.Name, path+".name")
			}
		}

		for j, alias := range cmd.Aliases {
			aPath := fmt.Sprintf("%s.aliases[%d]", path, j)
			if !reIdentifier.MatchString(alias) {
				errs.addKeywordf(aPath, "pattern", "alias %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", alias)
			}
			claim(alias, aPath)
		}

		validateDuration(cmd.Timeout, path+".timeout", errs)

		if cmd.Inputs != nil {
			validateInputs(cmd.Inputs, path, configFileNames, errs)
		}

		validateCommands(cmd.Commands, cmd.RemoteCommands, path, configFileNames, errs)
	}

	for i, rc := range remoteCmds {
		path := joinPath(parentPath, fmt.Sprintf("remote_commands[%d]", i))

		switch {
		case rc.Name == "":
			errs.addKeywordf(path+".name", "required", "remote command name is required")
		case !reIdentifier.MatchString(rc.Name):
			errs.addKeywordf(path+".name", "pattern", "%q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", rc.Name)
		default:
			claim(rc.Name, path+".name")
		}

		for j, alias := range rc.Aliases {
			aPath := fmt.Sprintf("%s.aliases[%d]", path, j)
			if !reIdentifier.MatchString(alias) {
				errs.addKeywordf(aPath, "pattern", "alias %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", alias)
			}
			claim(alias, aPath)
		}

		validateDuration(rc.Timeout, path+".timeout", errs)
	}
}

// validateInputs checks all input kinds for a command (or the root
// definition). It enforces:
//
//   - unique parameter names within the (combined) flags / arguments /
//     files / variables set
//   - flag identifier uniqueness within the same command
//   - argument variadic-must-be-last
//   - inputs.files[].schema.file references resolve
//   - inputs.variables[].schema.variable matches the env-var pattern
//
// cmdPath is the dotted path to the command (e.g., "commands[0]" or "").
func validateInputs(inputs *Inputs, cmdPath string, configFileNames map[string]bool, errs *SpecError) {
	seenNames := make(map[string]bool)

	checkName := func(name, path string) {
		if name == "" {
			errs.addKeywordf(path+".name", "required", "parameter name is required")
			return
		}
		if !reIdentifier.MatchString(name) {
			errs.addKeywordf(path+".name", "pattern", "%q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", name)
		}
		if seenNames[name] {
			errs.addKeywordf(path+".name", "duplicate", "duplicate parameter name %q", name)
		}
		seenNames[name] = true
	}

	inputsPath := joinInputs(cmdPath)

	seenIDs := make(map[string]string) // identifier → owning flag name
	for i := range inputs.Flags {
		p := &inputs.Flags[i]
		path := fmt.Sprintf("%s.flags[%d]", inputsPath, i)
		checkName(p.Name, path)
		for k, id := range p.Identifiers {
			idPath := fmt.Sprintf("%s.identifiers[%d]", path, k)
			if !reFlagID.MatchString(id) {
				errs.addKeywordf(idPath, "pattern", "%q must match pattern ^-{1,2}[a-zA-Z][a-zA-Z0-9_-]*$", id)
			}
			if owner, exists := seenIDs[id]; exists {
				errs.addKeywordf(idPath, "duplicate", "identifier %q is already used by flag %q", id, owner)
			} else {
				seenIDs[id] = p.Name
			}
		}
	}

	variadicSeen := false
	for i := range inputs.Arguments {
		p := &inputs.Arguments[i]
		path := fmt.Sprintf("%s.arguments[%d]", inputsPath, i)
		checkName(p.Name, path)
		if variadicSeen {
			errs.addKeywordf(path, "variadic", "argument %q appears after a variadic (array) argument", p.Name)
		}
		if p.Schema != nil && p.Schema.Type == "array" {
			variadicSeen = true
		}
	}

	for i := range inputs.Files {
		p := &inputs.Files[i]
		path := fmt.Sprintf("%s.files[%d]", inputsPath, i)
		checkName(p.Name, path)
		if p.Schema != nil {
			if p.Schema.File != "" && !configFileNames[p.Schema.File] {
				errs.addKeywordf(path+".schema.file", "ref", "%q does not match any declared files name", p.Schema.File)
			}
			if p.Schema.File != "" && p.Schema.Key != "" && !reConfigKey.MatchString(p.Schema.Key) {
				errs.addKeywordf(path+".schema.key", "pattern", "%q is not a valid key path; must match ^[a-zA-Z_][a-zA-Z0-9_.]*$", p.Schema.Key)
			}
		}
	}

	for i := range inputs.Variables {
		p := &inputs.Variables[i]
		path := fmt.Sprintf("%s.variables[%d]", inputsPath, i)
		checkName(p.Name, path)
		if p.Schema != nil && p.Schema.Variable != "" && !reEnvVar.MatchString(p.Schema.Variable) {
			errs.addKeywordf(path+".schema.variable", "pattern", "%q must match pattern ^[A-Z_][A-Z0-9_]*$", p.Schema.Variable)
		}
	}
}

// validateEventNames checks events[] for valid snake_case names and
// duplicates.
func validateEventNames(events []EventSpec, errs *SpecError) {
	seen := make(map[string]string)
	for i, ev := range events {
		if ev.Name == "" {
			continue
		}
		path := fmt.Sprintf("events[%d].name", i)
		if !reEventKey.MatchString(ev.Name) {
			errs.addKeywordf(path, "pattern", "event name %q must match pattern ^[a-z][a-z0-9_]*$", ev.Name)
		}
		if first, dup := seen[ev.Name]; dup {
			errs.addKeywordf(path, "duplicate", "duplicate event name %q (first declared at %s)", ev.Name, first)
		} else {
			seen[ev.Name] = path
		}
	}
}

// validateMetadataEntries checks metadata[] for valid exported Go
// identifiers and duplicates.
func validateMetadataEntries(entries []MetadataEntry, errs *SpecError) {
	seen := make(map[string]bool)
	for i, m := range entries {
		path := fmt.Sprintf("metadata[%d]", i)
		if m.Var == "" {
			errs.addKeywordf(path+".var", "required", "metadata var name is required")
			continue
		}
		if !reExported.MatchString(m.Var) {
			errs.addKeywordf(path+".var", "pattern", "%q must be a valid exported Go identifier (e.g., \"Version\", \"BuildDate\")", m.Var)
		}
		if seen[m.Var] {
			errs.addKeywordf(path+".var", "duplicate", "duplicate metadata var %q", m.Var)
		}
		seen[m.Var] = true
	}
}

// validateRefs walks the full spec tree and verifies that every $ref
// string resolves to a declared schema.
func validateRefs(s *Spec, errs *SpecError) {
	if len(s.Schemas) == 0 {
		return
	}
	schemas := s.Schemas

	checkRef := func(ref, location string) {
		if ref == "" {
			return
		}
		if _, ok := lookupSchemaRef(ref, schemas); !ok {
			errs.addKeywordf(location, "ref", "$ref %q does not match any declared schema", ref)
		}
	}

	checkSchemaRefs := func(sc *FieldSchema, location string) {
		if sc != nil {
			checkRef(sc.Ref, location)
		}
	}

	checkInputRefs := func(inp *Inputs, prefix string) {
		if inp == nil {
			return
		}
		for i := range inp.Flags {
			checkSchemaRefs(inp.Flags[i].Schema, fmt.Sprintf("%s.flags[%d].schema", prefix, i))
		}
		for i := range inp.Arguments {
			checkSchemaRefs(inp.Arguments[i].Schema, fmt.Sprintf("%s.arguments[%d].schema", prefix, i))
		}
		for i := range inp.Files {
			checkSchemaRefs(inp.Files[i].Schema, fmt.Sprintf("%s.files[%d].schema", prefix, i))
		}
		for i := range inp.Variables {
			checkSchemaRefs(inp.Variables[i].Schema, fmt.Sprintf("%s.variables[%d].schema", prefix, i))
		}
		if inp.Stdin != nil {
			checkSchemaRefs(inp.Stdin.Schema, prefix+".stdin.schema")
		}
	}

	// Root-level inputs.
	checkInputRefs(s.Inputs, "inputs")

	// Root-level events.
	for i, ev := range s.Events {
		checkSchemaRefs(ev.Schema, fmt.Sprintf("events[%d].schema", i))
	}

	// Walk commands recursively.
	var walkCmds func(cmds []Command, parentPath string)
	walkCmds = func(cmds []Command, parentPath string) {
		for i := range cmds {
			cmd := &cmds[i]
			path := joinPath(parentPath, fmt.Sprintf("commands[%d]", i))
			checkInputRefs(cmd.Inputs, path+".inputs")
			walkCmds(cmd.Commands, path)
		}
	}
	walkCmds(s.Commands, "")
}

// lookupSchemaRef resolves a "#/schemas/<Name>" reference against the
// top-level schemas map. Returns false on unknown prefix or missing
// name.
func lookupSchemaRef(ref string, schemas map[string]*FieldSchema) (*FieldSchema, bool) {
	const prefix = "#/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, false
	}
	name := strings.TrimPrefix(ref, prefix)
	sc, ok := schemas[name]
	return sc, ok
}

// joinPath concatenates a parent path and a child segment, omitting the
// dot when parent is empty.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// joinInputs produces the dotted location of an inputs block for a
// command. cmdPath may be empty (root) or e.g., "commands[0]".
func joinInputs(cmdPath string) string {
	if cmdPath == "" {
		return "inputs"
	}
	return cmdPath + ".inputs"
}
