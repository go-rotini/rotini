package internal

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/go-rotini/rotini/rtk"
)

// ToProgramSpec walks s and produces a [rtk.ProgramSpec] value. Templates
// (M2.5) render off this shape — they never see the DSL types directly.
//
// Translation rules:
//
//   - Command paths use hyphen joining: `""` for root, `"generate"` for a
//     direct child, `"foo-bar-baz"` for a three-level nested command.
//   - JSON-Schema-flavored type names (`"boolean"`, `"integer"`, `"number"`,
//     `"array"`, `"object"`, `"duration"`) are converted to their Go-form
//     equivalents (`"bool"`, `"int"`, `"float64"`, `"[]string"`, `"map"`,
//     `"time.Duration"`). Names already in Go form pass through unchanged.
//   - Each DSL input becomes an rtk surface element:
//   - `flags`     → [rtk.FlagSpec]
//   - `variables` → [rtk.FlagSpec] with `EnvOnly: true` and `EnvKey` set
//     (no CLI identifiers)
//   - `files`     → [rtk.FlagSpec] with `ConfigKey` set (no CLI
//     identifiers; argv-supplied still wins per the precedence chain)
//   - `arguments` → [rtk.ArgumentSpec], with Variadic inferred from a
//     slice-typed schema (`[]string`, `[]int`, etc.)
//   - Stdin format is inferred from the schema type: `"string"` → `"text"`,
//     anything else → `"json"`. Structured-stdin fields are derived from
//     `schema.properties`.
//
// ToProgramSpec assumes s has already passed [Validate]; it does no
// validation of its own. Behavior on invalid input is unspecified.
func ToProgramSpec(s *Spec) rtk.ProgramSpec {
	if s == nil {
		return rtk.ProgramSpec{}
	}
	out := rtk.ProgramSpec{
		Name:     s.Name,
		Flags:    convertFlags(s.Inputs),
		Commands: convertCommands(s.Commands, ""),
	}
	if s.Inputs != nil && s.Inputs.Stdin != nil {
		out.RootStdin = convertStdin(s.Inputs.Stdin)
	}
	return out
}

// convertCommands walks the spec's command tree, producing one
// [rtk.CommandSpec] per node. parentPath is the hyphen-joined path of
// the parent command (empty for the root level).
func convertCommands(cmds []Command, parentPath string) []rtk.CommandSpec {
	if len(cmds) == 0 {
		return nil
	}
	out := make([]rtk.CommandSpec, len(cmds))
	for i := range cmds {
		c := &cmds[i]
		path := c.Name
		if parentPath != "" {
			path = parentPath + "-" + c.Name
		}
		spec := rtk.CommandSpec{
			Path:      path,
			Name:      c.Name,
			Aliases:   c.Aliases,
			Flags:     convertFlags(c.Inputs),
			Arguments: convertArguments(c.Inputs),
			Commands:  convertCommands(c.Commands, path),
			Timeout:   parseDuration(c.Timeout),
		}
		if c.Inputs != nil && c.Inputs.Stdin != nil {
			spec.Stdin = convertStdin(c.Inputs.Stdin)
		}
		out[i] = spec
	}
	return out
}

// convertFlags merges the three flag-shaped input kinds (flags,
// variables, files) into a single [rtk.FlagSpec] slice. Order: flags
// first (in declaration order), then variables, then files. This matches
// rotiniold's convertInputs ordering so handler-visible Inputs structs
// list fields in the same order across the rewrite boundary.
func convertFlags(inputs *Inputs) []rtk.FlagSpec {
	if inputs == nil {
		return nil
	}
	total := len(inputs.Flags) + len(inputs.Variables) + len(inputs.Files)
	if total == 0 {
		return nil
	}
	out := make([]rtk.FlagSpec, 0, total)
	for i := range inputs.Flags {
		out = append(out, paramToFlagSpec(&inputs.Flags[i]))
	}
	for i := range inputs.Variables {
		out = append(out, paramToEnvFlagSpec(&inputs.Variables[i]))
	}
	for i := range inputs.Files {
		out = append(out, paramToConfigFlagSpec(&inputs.Files[i]))
	}
	return out
}

// convertArguments converts the DSL arguments array to [rtk.ArgumentSpec]
// slice. Variadic is inferred from a slice-typed schema (Go type name
// starting with `[]`).
func convertArguments(inputs *Inputs) []rtk.ArgumentSpec {
	if inputs == nil || len(inputs.Arguments) == 0 {
		return nil
	}
	out := make([]rtk.ArgumentSpec, len(inputs.Arguments))
	for i := range inputs.Arguments {
		p := &inputs.Arguments[i]
		typ := schemaTypeToGo(p.Schema)
		spec := rtk.ArgumentSpec{
			Name:     p.Name,
			Type:     typ,
			Variadic: strings.HasPrefix(typ, "[]"),
		}
		if p.Schema != nil {
			spec.Required = p.Schema.RequiredBool()
			spec.Nullable = p.Schema.Nullable
			spec.Default = defaultStr(p.Schema.Default)
			spec.Enum = p.Schema.Enum
			spec.Pattern = p.Schema.Pattern
			spec.Min = p.Schema.Minimum
			spec.Max = p.Schema.Maximum
			spec.MinLength = p.Schema.MinLength
			spec.MaxLength = p.Schema.MaxLength
			spec.MinItems = p.Schema.MinItems
			spec.MaxItems = p.Schema.MaxItems
			spec.EnvKey = p.Schema.Variable
			spec.ConfigKey = p.Schema.Key
			spec.Description = p.Schema.Description
		}
		out[i] = spec
	}
	return out
}

// paramToFlagSpec converts a flag parameter. If the user omitted
// `identifiers`, a single canonical `--<name>` form is auto-derived (with
// underscores swapped for hyphens, matching the convention rotiniold and
// most CLI conventions use).
func paramToFlagSpec(p *Parameter) rtk.FlagSpec {
	identifiers := p.Identifiers
	if len(identifiers) == 0 {
		identifiers = []string{"--" + strings.ReplaceAll(p.Name, "_", "-")}
	}
	return populateFlagSpec(p, identifiers, false /*envOnly*/)
}

// paramToEnvFlagSpec converts an environment-variable input parameter
// into a [rtk.FlagSpec] without CLI identifiers. EnvOnly=true so the
// parser rejects argv-supplied values for this input.
func paramToEnvFlagSpec(p *Parameter) rtk.FlagSpec {
	spec := populateFlagSpec(p, nil /*no identifiers*/, true /*envOnly*/)
	if spec.EnvKey == "" && p.Schema != nil {
		// Variables sourced from a `variables:` block: the schema's
		// Variable field is the env-var name. Fall back to it when the
		// inputs declaration didn't otherwise set EnvKey.
		spec.EnvKey = p.Schema.Variable
	}
	return spec
}

// paramToConfigFlagSpec converts a config-file-value input parameter.
// The resulting FlagSpec has no CLI identifiers (config values are
// read-only inputs from the standpoint of the CLI); argv-supplied values
// would be rejected by the spec's intent, so EnvOnly is left false and
// the parser handles "no identifiers" by skipping argv resolution for
// this flag.
func paramToConfigFlagSpec(p *Parameter) rtk.FlagSpec {
	spec := populateFlagSpec(p, nil, false)
	if spec.ConfigKey == "" && p.Schema != nil {
		// ConfigKey comes from schema.key — fall back to it when the
		// inputs declaration didn't otherwise set ConfigKey.
		spec.ConfigKey = p.Schema.Key
	}
	return spec
}

// populateFlagSpec is the shared core of paramTo*FlagSpec — it fills in
// the constraint fields from p.Schema. The caller decides what
// identifiers and EnvOnly to set.
func populateFlagSpec(p *Parameter, identifiers []string, envOnly bool) rtk.FlagSpec {
	spec := rtk.FlagSpec{
		Name:        p.Name,
		Identifiers: identifiers,
		Type:        schemaTypeToGo(p.Schema),
		EnvOnly:     envOnly,
	}
	if p.Schema != nil {
		spec.Description = p.Schema.Description
		spec.Required = p.Schema.RequiredBool()
		spec.Nullable = p.Schema.Nullable
		spec.Default = defaultStr(p.Schema.Default)
		spec.Enum = p.Schema.Enum
		spec.Pattern = p.Schema.Pattern
		spec.Min = p.Schema.Minimum
		spec.Max = p.Schema.Maximum
		spec.MinLength = p.Schema.MinLength
		spec.MaxLength = p.Schema.MaxLength
		spec.MinItems = p.Schema.MinItems
		spec.MaxItems = p.Schema.MaxItems
		spec.EnvKey = p.Schema.Variable
		spec.ConfigKey = p.Schema.Key
	}
	return spec
}

// convertStdin maps a DSL StdinSpec to a [rtk.StdinSpec]. Format is
// inferred:
//
//   - schema.type == "string" → "text"
//   - schema.type unset or anything else → "json"
//
// (The DSL doesn't yet carry an explicit format field for "raw" /
// "yaml" / "toml" / "jsonc" — extending the schema is M5+ work.)
//
// Structured-stdin fields are emitted in alphabetical order to keep
// generated code deterministic across runs.
func convertStdin(in *StdinSpec) *rtk.StdinSpec {
	if in == nil {
		return nil
	}
	format := "json"
	if in.Schema != nil && in.Schema.Type == "string" {
		format = "text"
	}
	out := &rtk.StdinSpec{Format: format}
	if in.Schema != nil {
		out.Fields = schemaPropertiesToStdinFields(in.Schema.Properties)
	}
	return out
}

// schemaPropertiesToStdinFields converts a map of object-properties to
// the slice form [rtk.StdinField] uses. Order is alphabetical for
// deterministic codegen output.
func schemaPropertiesToStdinFields(props map[string]*FieldSchema) []rtk.StdinField {
	if len(props) == 0 {
		return nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]rtk.StdinField, 0, len(names))
	for _, name := range names {
		p := props[name]
		if p == nil {
			continue
		}
		out = append(out, rtk.StdinField{
			Name:     name,
			Type:     schemaTypeToGo(p),
			Required: p.RequiredBool(),
		})
	}
	return out
}

// schemaTypeToGo returns the Go-flavored type name for s.Type. JSON-
// Schema names ("boolean", "integer", "number", "array", "object",
// "duration", "time"/"datetime"/"date") are converted; Go-flavored names
// already in canonical form pass through.
//
// When s is nil or s.Type is empty, "string" is returned — string is
// rotini's default flag/argument type.
func schemaTypeToGo(s *FieldSchema) string {
	if s == nil || s.Type == "" {
		return "string"
	}
	switch s.Type {
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "array":
		return "[]string"
	case "object":
		return "map"
	case "duration":
		return "time.Duration"
	case "time", "datetime", "date":
		return "time.Time"
	default:
		return s.Type
	}
}

// defaultStr renders a json.RawMessage default value as a string. JSON
// strings are unquoted; numbers and booleans pass through verbatim
// (their JSON representation is already the string form the coerce
// layer parses).
func defaultStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// parseDuration parses a Go duration string from a spec field. Returns
// 0 (zero duration) on empty or unparseable input. [Validate] catches
// the unparseable case so by the time ToProgramSpec runs, only "valid"
// or "" reaches here.
func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
