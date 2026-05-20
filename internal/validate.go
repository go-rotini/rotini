package internal

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Compiled patterns matching the rotini-schema.json constraints.
var (
	reIdentifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
	reEventKey   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reFlagID     = regexp.MustCompile(`^-{1,2}[a-zA-Z][a-zA-Z0-9_-]*$`)
	reEnvVar     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	reExported   = regexp.MustCompile(`^[A-Z][a-zA-Z0-9_]*$`)
	reConfigKey  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)
)

// validConfigFormats is the set of allowed values for configSpec.Format.
var validConfigFormats = map[string]bool{"json": true, "yaml": true, "toml": true}

// errList accumulates validation errors.
type errList []error

func (e *errList) addf(format string, args ...any) {
	*e = append(*e, fmt.Errorf(format, args...))
}

// Validate loads the spec and conf files and validates them, returning whether
// the spec is valid. It is used as a pre-flight check by both the validate and
// generate CLI commands. If confPath is empty, the default conf file is
// discovered automatically.
func Validate(specPath, confPath string) (bool, error) {
	s, err := newSpec(resolveSpecPath(specPath), confPath)
	if err != nil {
		return false, err
	}

	if errs := checkSpec(s); len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	return true, nil
}

// checkSpec performs static analysis on an already-loaded spec and returns all
// errors that would prevent the rotini framework from functioning correctly.
func checkSpec(spec *spec) []error {
	var errs errList

	validateDefinition(spec, &errs)

	return []error(errs)
}

// validateDuration validates that s is a non-empty valid Go duration string.
// Empty strings are silently accepted (they mean "no duration specified").
func validateDuration(s, path string, errs *errList) {
	if s == "" {
		return
	}
	if _, err := time.ParseDuration(s); err != nil {
		errs.addf("[%s] %q is not a valid Go duration string (e.g. \"10s\", \"1m30s\"): %v", path, s, err)
	}
}

// validateDefinition validates the top-level spec and all nested structures.
func validateDefinition(spec *spec, errs *errList) {
	def := &spec.Definition

	if def.Name == "" {
		errs.addf("[definition.name] command name is required")
	} else if !reIdentifier.MatchString(def.Name) {
		errs.addf("[definition.name] %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", def.Name)
	}

	// #13: validate root-level timeout.
	validateDuration(def.Timeout, "definition", errs)

	configFileNames := validateConfigSpecs(def.Files, errs)

	if def.Inputs != nil {
		validateInputs(def.Inputs, "definition", configFileNames, errs)
	}

	validateCommands(def.Commands, def.RemoteCommands, "", configFileNames, errs)
	validateEventNames(def.Events, errs)
	validateMetadataEntries(def.Metadata, errs)

	// #14: validate all $ref targets resolve against declared schemas.
	validateRefs(spec, errs)
}

// validateConfigSpecs checks files entries and returns the set of declared names.
func validateConfigSpecs(specs []configSpec, errs *errList) map[string]bool {
	names := make(map[string]bool, len(specs))
	for i, cs := range specs {
		path := fmt.Sprintf("files[%d]", i)
		if cs.Name == "" {
			errs.addf("[%s.name] configuration file name is required", path)
		} else {
			if names[cs.Name] {
				errs.addf("[%s.name] duplicate configuration file name %q", path, cs.Name)
			}
			names[cs.Name] = true
		}
		if cs.Path == "" {
			errs.addf("[%s.path] configuration file path is required", path)
		}
		if cs.Format != "" && !validConfigFormats[cs.Format] {
			errs.addf("[%s.format] %q is not valid; must be \"json\", \"yaml\", or \"toml\"", path, cs.Format)
		}
	}
	return names
}

// validateCommands checks command names, aliases, inputs, and recurses into subcommands.
// It validates both regular commands and remote commands at the same routing level.
func validateCommands(cmds []command, remoteCmds []remoteCommand, parentPath string, configFileNames map[string]bool, errs *errList) {
	// All names and aliases at this level share a single routing namespace.
	seen := make(map[string]string) // token → "commands[i].name" / "commands[i].aliases[j]" / "remote_commands[i].name"

	claim := func(token, owner string) {
		if first, dup := seen[token]; dup {
			errs.addf("[%s] %q clashes with %s in the same command namespace", owner, token, first)
		} else {
			seen[token] = owner
		}
	}

	for i, cmd := range cmds {
		path := joinPath(parentPath, fmt.Sprintf("commands[%d]", i))

		if cmd.Name == "" {
			errs.addf("[%s.name] command name is required", path)
		} else {
			if !reIdentifier.MatchString(cmd.Name) {
				errs.addf("[%s.name] %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", path, cmd.Name)
			}
			claim(cmd.Name, path+".name")
		}

		for j, alias := range cmd.Aliases {
			aPath := fmt.Sprintf("%s.aliases[%d]", path, j)
			if !reIdentifier.MatchString(alias) {
				errs.addf("[%s] alias %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", aPath, alias)
			}
			claim(alias, aPath)
		}

		// #13: validate command timeout.
		validateDuration(cmd.Timeout, path+".timeout", errs)

		if cmd.Inputs != nil {
			validateInputs(cmd.Inputs, path, configFileNames, errs)
		}

		validateCommands(cmd.Commands, cmd.RemoteCommands, path, configFileNames, errs)
	}

	for i, rc := range remoteCmds {
		path := joinPath(parentPath, fmt.Sprintf("remote_commands[%d]", i))

		if rc.Name == "" {
			errs.addf("[%s.name] remote command name is required", path)
		} else {
			if !reIdentifier.MatchString(rc.Name) {
				errs.addf("[%s.name] %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", path, rc.Name)
			}
			claim(rc.Name, path+".name")
		}

		for j, alias := range rc.Aliases {
			aPath := fmt.Sprintf("%s.aliases[%d]", path, j)
			if !reIdentifier.MatchString(alias) {
				errs.addf("[%s] alias %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", aPath, alias)
			}
			claim(alias, aPath)
		}

		// #13: validate remote command timeout.
		validateDuration(rc.Timeout, path+".timeout", errs)
	}
}

// validateInputs checks all input kinds for a command or root definition.
func validateInputs(inputs *inputs, cmdPath string, configFileNames map[string]bool, errs *errList) {
	seenNames := make(map[string]bool)

	checkName := func(name, path string) {
		if name == "" {
			errs.addf("[%s.name] parameter name is required", path)
			return
		}
		if !reIdentifier.MatchString(name) {
			errs.addf("[%s.name] %q must match pattern ^[a-zA-Z][a-zA-Z0-9_-]*$", path, name)
		}
		if seenNames[name] {
			errs.addf("[%s.name] duplicate parameter name %q", path, name)
		}
		seenNames[name] = true
	}

	seenIDs := make(map[string]string) // identifier → owning flag name
	for i := range inputs.Flags {
		p := &inputs.Flags[i]
		path := fmt.Sprintf("%s.inputs.flags[%d]", cmdPath, i)
		checkName(p.Name, path)
		for k, id := range p.Identifiers {
			idPath := fmt.Sprintf("%s.identifiers[%d]", path, k)
			if !reFlagID.MatchString(id) {
				errs.addf("[%s] %q must match pattern ^-{1,2}[a-zA-Z][a-zA-Z0-9_-]*$", idPath, id)
			}
			if owner, exists := seenIDs[id]; exists {
				errs.addf("[%s] identifier %q is already used by flag %q", idPath, id, owner)
			} else {
				seenIDs[id] = p.Name
			}
		}
	}

	variadicSeen := false
	for i := range inputs.Arguments {
		p := &inputs.Arguments[i]
		path := fmt.Sprintf("%s.inputs.arguments[%d]", cmdPath, i)
		checkName(p.Name, path)
		if variadicSeen {
			errs.addf("[%s] argument %q appears after a variadic (array) argument", path, p.Name)
		}
		if p.Schema != nil && p.Schema.Type == "array" {
			variadicSeen = true
		}
	}

	for i := range inputs.Files {
		p := &inputs.Files[i]
		path := fmt.Sprintf("%s.inputs.files[%d]", cmdPath, i)
		checkName(p.Name, path)
		if p.Schema != nil {
			if p.Schema.File != "" && !configFileNames[p.Schema.File] {
				errs.addf("[%s.schema.file] %q does not match any declared files name", path, p.Schema.File)
			}
			if p.Schema.File != "" && p.Schema.Key != "" && !reConfigKey.MatchString(p.Schema.Key) {
				errs.addf("[%s.schema.key] %q is not a valid key path; must match ^[a-zA-Z_][a-zA-Z0-9_.]*$", path, p.Schema.Key)
			}
		}
	}

	for i := range inputs.Variables {
		p := &inputs.Variables[i]
		path := fmt.Sprintf("%s.inputs.variables[%d]", cmdPath, i)
		checkName(p.Name, path)
		if p.Schema != nil && p.Schema.Variable != "" && !reEnvVar.MatchString(p.Schema.Variable) {
			errs.addf("[%s.schema.variable] %q must match pattern ^[A-Z_][A-Z0-9_]*$", path, p.Schema.Variable)
		}
	}

}

// validateEventNames checks that all event names are unique and match the required format.
func validateEventNames(events []eventSpec, errs *errList) {
	seen := make(map[string]string) // name → first path

	for i, ev := range events {
		if ev.Name == "" {
			continue
		}
		path := fmt.Sprintf("events[%d].name", i)
		if !reEventKey.MatchString(ev.Name) {
			errs.addf("[%s] event name %q must match pattern ^[a-z][a-z0-9_]*$", path, ev.Name)
		}
		if first, dup := seen[ev.Name]; dup {
			errs.addf("[%s] duplicate event name %q (first declared at %s)", path, ev.Name, first)
		} else {
			seen[ev.Name] = path
		}
	}
}

// validateMetadataEntries checks metadata entries for valid exported Go identifiers and duplicates.
func validateMetadataEntries(entries []metadataEntry, errs *errList) {
	seen := make(map[string]bool)
	for i, m := range entries {
		path := fmt.Sprintf("metadata[%d]", i)
		if m.Var == "" {
			errs.addf("[%s.var] metadata var name is required", path)
			continue
		}
		if !reExported.MatchString(m.Var) {
			errs.addf("[%s.var] %q must be a valid exported Go identifier (e.g. \"Version\", \"BuildDate\")", path, m.Var)
		}
		if seen[m.Var] {
			errs.addf("[%s.var] duplicate metadata var %q", path, m.Var)
		}
		seen[m.Var] = true
	}
}

// validateRefs walks the full spec tree and verifies that every $ref string
// resolves to a declared schema. This runs at validation time so authors get
// a clear error before resolveRefs (which only runs during spec load) has a chance to error.
func validateRefs(s *spec, errs *errList) {
	if len(s.Schemas) == 0 {
		return
	}
	schemas := s.Schemas

	checkRef := func(ref, path string) {
		if ref == "" {
			return
		}
		if _, ok := lookupSchemaRef(ref, schemas); !ok {
			errs.addf("[%s] $ref %q does not match any declared schema", path, ref)
		}
	}

	checkSchemaRefs := func(sc *schema, path string) {
		if sc != nil {
			checkRef(sc.Ref, path)
		}
	}

	checkInputRefs := func(inp *inputs, prefix string) {
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
	checkInputRefs(s.Definition.Inputs, "definition.inputs")

	// Root-level events.
	for i, ev := range s.Definition.Events {
		checkSchemaRefs(ev.Schema, fmt.Sprintf("definition.events[%d].schema", i))
	}

	// Walk commands recursively.
	var walkCmds func(cmds []command, parentPath string)
	walkCmds = func(cmds []command, parentPath string) {
		for i, cmd := range cmds {
			path := joinPath(parentPath, fmt.Sprintf("commands[%d]", i))
			checkInputRefs(cmd.Inputs, path+".input")
			walkCmds(cmd.Commands, path)
		}
	}
	walkCmds(s.Definition.Commands, "")
}

// joinPath concatenates a parent path and a child segment, omitting the dot when parent is empty.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
