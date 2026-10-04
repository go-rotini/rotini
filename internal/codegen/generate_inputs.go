package codegen

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Typed-input field derivation: turning a command's Inputs (flags/args/env/config/
// stdin) into the generated struct fields, plus the schema→Go type mapping. The Go-
// literal emission those fields feed into lives in generate_literals.go.

// inputsFields returns the fields of a command's <Prefix>Inputs struct: one per ancestor plus
// the command itself, in root→leaf order, each named after the command's PascalCase prefix.
//
// There is deliberately no struct tag naming the command. The input reader maps fields to chain
// frames by POSITION, counting back from the frame whose hook is running, so field names are
// labels for a reader and never matched against anything.
//
// Tagging them was considered as a way to make that mapping checkable — a field could then be
// verified against the frame it landed on, closing the last case where a mismatched inputs type
// binds quietly. It does not work: a composed child is RENAMED by the umbrella that grafts it
// (`- $ref: ../child/… ` with `name: kid`), while the child's types are generated in the child's
// own repo under its own names. The tag would say "child" and the frame would say "kid", so the
// check would fire on exactly the composition it is supposed to protect. Closing that case needs
// the frame to carry a grafted command's ORIGIN identity, which nothing does today.
func inputsFields(rootPascal, path string) []fieldDef {
	segments := strings.Split(path, "_")
	fields := make([]fieldDef, 0, 1+len(segments))
	fields = append(fields, fieldDef{Field: rootPascal, GoType: rootPascal + "CommandInputs"})
	for i := 1; i <= len(segments); i++ {
		prefix := rootPascal + toPascalCase(strings.Join(segments[:i], "_"))
		fields = append(fields, fieldDef{Field: prefix, GoType: prefix + "CommandInputs"})
	}
	return fields
}

// flagFields returns the <Prefix>Flags struct fields for a command's inputs: the
// argv flags. The env/config channels are their own structs (envFields/configFields);
// a flag with an env/config *fallback* still lives here and is reconciled by the input reader.
func flagFields(in *Inputs, envPrefix string) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Flags))
	for _, f := range in.Flags {
		key := flagReconKey(f.Name, f.Schema)
		fields = append(fields, fieldDef{
			Field: toPascalCase(f.Name), GoType: goFieldType(f.Schema), Tag: f.Name,
			Import: fieldImport(f.Schema), Recon: key,
			// A flag's env fallback is pinned, not derived at bind time — the same
			// reason env inputs are; see [envVarFor]. An argv-only flag has no recon
			// key and so no env fallback to name.
			EnvVar:  flagEnvVar(f.Schema, key, envPrefix),
			Comment: contractComment(f.Schema),
		})
	}
	return fields
}

// flagReconKey is a flag's reconciliation key, which is what opts it into the fallback chain
// (argv > env > config > default). An argv-only flag has none and gets no recon tag.
//
// `key:` names it outright. `variable:` alone also opts in, keyed by the flag's own name —
// the same default a config input uses — with the env variable pinned to the declared name
// rather than derived. That is what lets --token read GITHUB_TOKEN without inventing a config
// key called github.token, which was the only way to spell it before.
func flagReconKey(name string, schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if schema.Key != "" {
		return schema.Key
	}
	if len(variables(schema)) > 0 {
		return name
	}
	return ""
}

// envFields returns the <Prefix>Env struct fields: one per pure environment input. The recon
// key is the input name, and the variable is always written into the field's `env:` tag (see
// envVarFor), so the input reader never re-derives it.
func envFields(in *Inputs, envPrefix string) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Env))
	for _, e := range in.Env {
		fd := fieldDef{
			Field: toPascalCase(e.Name), GoType: goFieldType(e.Schema), Tag: e.Name,
			Import: fieldImport(e.Schema), Recon: reconTag(e.Name, e.Schema),
			// Always pinned, explicit or derived — never left for recon to re-derive.
			EnvVar:     envVarName(e, envPrefix),
			Constraint: constraintTags(e.Schema),
		}
		// A nested input's variable is a family PREFIX, not the value's own env
		// var — it rides in the envnest tag instead of env:, and rotini (not
		// recon) enforces required, since recon resolves leaf keys only.
		if e.Schema != nil && e.Schema.Nesting != "" {
			// lintVariable allows one name here: a family prefix is not a choice of names.
			fd.EnvNest = envVarName(e, envPrefix) + "," + e.Schema.Nesting
			if e.Schema.Required {
				fd.EnvNest += ",required"
			}
			fd.EnvVar = ""
			fd.Recon = nestedReconTag(e.Name, e.Schema)
		}
		fields = append(fields, fd)
	}
	return fields
}

// constraintTags renders an input's numeric/string/array constraints as space-separated
// validation struct-tags (e.g. `min:"1" max:"65535" pattern:"^x$"`) for the input reader to
// enforce, or "" when none are set. Mirrors the FlagDef/ArgDef constraints the parser enforces
// for argv, but carried on the env/config field itself since channels have no Definition.
//
// Every value is written as a quoted Go string, because reflect reads a tag value back through
// strconv.Unquote: a pattern written raw, like ^\d+$, is an invalid escape there, and the tag
// silently read as absent — the pattern was never enforced. An enum rides along as a JSON array
// so a member may hold any character.
func constraintTags(schema *InputSchema) string {
	var parts []string
	eachConstraint(schema, func(tag, _, tagVal, _ string) {
		parts = append(parts, tag+":"+strconv.Quote(tagVal))
	})
	if l := layoutFor(schema); l != "" {
		parts = append(parts, "layout:"+strconv.Quote(l))
	}
	if schema != nil && len(schema.Enum) > 0 {
		if members, err := json.Marshal(schema.Enum); err == nil { // a []string always marshals
			parts = append(parts, "enum:"+strconv.Quote(string(members)))
		}
		if schema.IgnoreCase {
			parts = append(parts, `ignorecase:"true"`)
		}
	}
	return strings.Join(parts, " ")
}

// eachConstraint visits every present validation constraint on schema in a stable order,
// passing its tag name, its Constraints field name, and the value rendered for both a struct
// tag and a Go literal. Both constraintTags and constraintsLiteral drive off it, so the two
// cannot drift in set or order.
func eachConstraint(schema *InputSchema, visit func(tag, field, tagVal, litVal string)) {
	if schema == nil {
		return
	}
	floatC := func(tag, field string, p *float64) {
		if p == nil {
			return
		}
		s := strconv.FormatFloat(*p, 'g', -1, 64)
		visit(tag, field, s, rotiniPkgName+".Ptr[float64]("+s+")")
	}
	intC := func(tag, field string, n int) {
		if n == 0 {
			return
		}
		s := strconv.Itoa(n)
		visit(tag, field, s, s)
	}
	floatC("min", "Minimum", bound(schema.Minimum))
	floatC("max", "Maximum", bound(schema.Maximum))
	floatC("xmin", "ExclusiveMinimum", bound(schema.ExclusiveMinimum))
	floatC("xmax", "ExclusiveMaximum", bound(schema.ExclusiveMaximum))
	floatC("multipleof", "MultipleOf", bound(schema.MultipleOf))
	intC("minlen", "MinLength", schema.MinLength)
	intC("maxlen", "MaxLength", schema.MaxLength)
	intC("minitems", "MinItems", schema.MinItems)
	intC("maxitems", "MaxItems", schema.MaxItems)
	if schema.Pattern != "" {
		visit("pattern", "Pattern", schema.Pattern, strconv.Quote(schema.Pattern))
		if schema.PatternMessage != "" {
			visit("patternmsg", "PatternMessage", schema.PatternMessage, strconv.Quote(schema.PatternMessage))
		}
	}
}

// envVarOf returns an input's explicit environment variable (schema.variable), or "" when it
// declares none and the caller derives one (envVarFor).
//
// Several names (`variable: [GH_TOKEN, GITHUB_TOKEN]`) ride in the one tag comma-joined; the
// input reader reads the first that is set.
func envVarOf(schema *InputSchema) string {
	return strings.Join(variables(schema), ",")
}

// flagEnvVar is a flag's pinned env-fallback variable: its explicit `variable:` when declared
// (exempt from env_prefix, because it is already exact), else the name derived from the recon
// key that opted it into the fallback chain. An argv-only flag has no key and no variable.
func flagEnvVar(schema *InputSchema, reconKey, envPrefix string) string {
	if v := envVarOf(schema); v != "" {
		return v
	}
	if reconKey == "" {
		return ""
	}
	return envVarFor(reconKey, envPrefix)
}

// configFields returns the <Prefix>Config struct fields: one per pure config-file
// input. The recon key is the declared key path (schema.key), else the input name.
func configFields(in *Inputs) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Config))
	for _, c := range in.Config {
		fd := fieldDef{
			Field: toPascalCase(c.Name), GoType: goFieldType(c.Schema), Tag: c.Name,
			Import: fieldImport(c.Schema), Recon: reconTag(configKey(c), c.Schema),
			Constraint: constraintTags(c.Schema),
		}
		if c.Schema != nil && c.Schema.File != "" {
			fd.CfgFile = c.Schema.File
		}
		fields = append(fields, fd)
	}
	return fields
}

// configKey is a config input's recon key: its declared schema.key, else its name.
func configKey(c ConfigInput) string {
	if c.Schema != nil && c.Schema.Key != "" {
		return c.Schema.Key
	}
	return c.Name
}

// nestedReconTag is the recon tag body for a nested env input: key + secret
// only — required/default are the envnest fill's concern (recon would judge
// them against a leaf key that never resolves).
func nestedReconTag(key string, schema *InputSchema) string {
	parts := []string{key}
	if schema.Secret {
		parts = append(parts, "secret")
	}
	return strings.Join(parts, ",")
}

// reconTag builds an env/config field's recon struct-tag body: the canonical key,
// then default=/required/secret from the input schema.
func reconTag(key string, schema *InputSchema) string {
	parts := []string{key}
	if schema != nil {
		if d := defaultString(schema.Default); d != "" {
			parts = append(parts, "default="+d)
		}
		if schema.Required {
			parts = append(parts, "required")
		}
		if schema.Secret {
			parts = append(parts, "secret")
		}
	}
	return strings.Join(parts, ",")
}

// stdinTypeExpr returns the Go type for a command's Stdin field: "*<Prefix>Stdin" for a
// document payload, and the pointed-to scalar for a RAW one — "*string" for text, "*[]string"
// for lines. "" when the command declares no stdin.
//
// A pointer either way, so "nothing was piped" stays distinguishable from "an empty payload
// was piped", which for a filter is a real difference.
func stdinTypeExpr(prefix string, in *Inputs) string {
	if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
		return ""
	}
	switch in.Stdin.Format {
	case "text":
		return "*string"
	case "lines":
		return "*[]string"
	}
	return "*" + prefix + "Stdin"
}

// rawStdinFormat reports whether a stdin format binds the payload directly rather than
// decoding it into a generated struct.
func rawStdinFormat(format string) bool { return format == "text" || format == "lines" }

// stdinFormatExpr returns the value of a command's `stdin:"<format>[,required]"`
// struct tag: the decode format (defaulting to json), with ",required" appended
// when the spec marks the payload required — the input reader then rejects an empty
// stdin instead of leaving the payload nil. "" when no stdin.
func stdinFormatExpr(in *Inputs) string {
	if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
		return ""
	}
	format := in.Stdin.Format
	if format == "" {
		format = "json"
	}
	if in.Stdin.Schema.Required {
		format += ",required"
	}
	return format
}

// argFields returns the <Prefix>Arguments struct fields for a command's inputs.
func argFields(in *Inputs) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Arguments))
	for _, a := range in.Arguments {
		fields = append(fields, fieldDef{Field: toPascalCase(a.Name), GoType: goFieldType(a.Schema), Tag: a.Name, Import: fieldImport(a.Schema), Comment: contractComment(a.Schema)})
	}
	return fields
}

// goFieldType resolves an input schema to a Go type expression, defaulting to
// string and applying a pointer for nullable inputs. The base type matches the
// Definition type string (getSchemaType); a nullable input wraps it in a pointer.
func goFieldType(schema *InputSchema) string {
	t := getSchemaType(schema)
	if schema != nil && schema.Nullable {
		return "*" + t
	}
	return t
}

// refTypeName returns the named-schema type for an intra-document "$ref"
// ("#/schemas/X" → "X"), or "" when ref is empty or external. The named type is
// generated from the document-level `schemas` map (see buildOutputTypes).
func refTypeName(ref string) string {
	if name, ok := strings.CutPrefix(ref, "#/schemas/"); ok {
		return name
	}
	return ""
}

// jsonSchemaTypeToGo maps a schema type name to a Go type expression. It
// accepts both JSON Schema standard names and Go names (the schema permits
// both); unknown values pass through unchanged so custom types are usable.
func jsonSchemaTypeToGo(t string) string {
	// A Go-style list or map spelling resolves the names INSIDE it too, so `[]bytesize` and
	// `map[string]duration` mean what they say. Before this only a bare name was translated, and
	// `[]duration` generated a `[]duration` field that failed to compile.
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "[]" + jsonSchemaTypeToGo(elem)
	}
	if key, val, ok := splitMapType(t); ok {
		return "map[" + jsonSchemaTypeToGo(key) + "]" + jsonSchemaTypeToGo(val)
	}
	switch t {
	case "boolean", "bool":
		return "bool"
	case "integer", "int":
		return "int"
	case "number", "float64":
		return "float64"
	case "array", "[]string":
		return "[]string"
	case "object", "map":
		return "map[string]any"
	case "count":
		return "int" // presence counter: the field tallies occurrences (-vvv → 3)
	case "existingfile", "existingdir":
		return "string" // the VALUE is a path; the type name is what makes the parser check it
	case "duration":
		return "time.Duration"
	case "time", "datetime", "date":
		return "time.Time"
	case "url":
		return "*url.URL"
	case "email":
		return "mail.Address"
	case "timezone":
		return "*time.Location"
	case "mac":
		return "net.HardwareAddr"
	case "ip":
		return "netip.Addr"
	case "cidr":
		return "netip.Prefix"
	case "hostport":
		return "netip.AddrPort"
	case "bytesize":
		return "rotini.ByteSize"
	case "hexbytes":
		return "rotini.HexBytes"
	case "base64bytes":
		return "rotini.Base64Bytes"
	default:
		return t
	}
}

// splitMapType splits a `map[K]V` spelling into its key and value, matching brackets so a key or
// value that itself contains brackets is kept whole.
func splitMapType(t string) (key, val string, ok bool) {
	rest, ok := strings.CutPrefix(t, "map[")
	if !ok {
		return "", "", false
	}
	depth := 1
	for i, r := range rest {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return rest[:i], rest[i+1:], rest[i+1:] != ""
			}
		}
	}
	return "", "", false
}

// rotiniTypeAliases are the type names a spec may write that are rotini's own vocabulary rather
// than Go's — every non-Go name jsonSchemaTypeToGo resolves. lintSchemaTypes uses it to tell a
// real alias from a typo, and TestRotiniTypeAliasesMatchTheResolver keeps it and the switch above
// in step.
var rotiniTypeAliases = []string{
	"boolean", "integer", "number", "array", "object", "map", "count",
	"existingfile", "existingdir", "duration", "time", "datetime", "date",
	"url", "email", "timezone", "mac", "ip", "cidr", "hostport",
	"bytesize", "hexbytes", "base64bytes",
}

// toTemplateFields converts resolved fieldDefs to renderer input fields,
// assembling each field's complete struct-tag literal from its parts. A fieldDef
// with no rotini tag (the <Prefix>Inputs fields, which the input reader maps by
// position) yields a field with no tag at all.
func toTemplateFields(fs []fieldDef) []templateInputField {
	out := make([]templateInputField, 0, len(fs))
	for _, f := range fs {
		tf := templateInputField{Field: f.Field, GoType: f.GoType, Comment: f.Comment}
		if f.Tag != "" {
			tf.Tag = inputFieldTag(f)
		}
		out = append(out, tf)
	}
	return out
}

// collectPathFrom maps each configuration_files name to the inputs supplying its path, across
// the whole tree: a flag claims by logical name, an env input by its variable. Validation
// guarantees single claims per channel and that the named entry exists.
func collectPathFrom(gp *program) map[string]pathFromClaim {
	out := map[string]pathFromClaim{}
	add := func(in *Inputs) {
		if in == nil {
			return
		}
		for _, f := range in.Flags {
			if f.Schema != nil && f.Schema.ConfigSource != "" {
				c := out[f.Schema.ConfigSource]
				c.flag = f.Name
				out[f.Schema.ConfigSource] = c
			}
		}
		for _, e := range in.Env {
			if e.Schema != nil && e.Schema.ConfigSource != "" {
				c := out[e.Schema.ConfigSource]
				c.env = envVarName(e, gp.envPrefix)
				out[e.Schema.ConfigSource] = c
			}
		}
	}
	add(gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.inputs) })
	return out
}

// contractComment is the TextUnmarshaler nudge emitted on flag and argument fields whose type
// comes from an explicit spec `import:`, putting the contract where the user reads their own
// generated code. Builtin aliases parse themselves, and env and config fields decode through
// recon, so neither gets one.
func contractComment(schema *InputSchema) string {
	if schema == nil || strings.TrimSpace(schema.Import) == "" {
		return ""
	}
	return "// parsed via its encoding.TextUnmarshaler (see the spec schema's `type` docs)"
}

// envVarName is an env input's environment variable: the explicit `variable:` when declared,
// else the derived name [envVarFor] builds from its logical name.
func envVarName(e EnvInput, envPrefix string) string {
	if v := envVarOf(e.Schema); v != "" {
		return v // explicit variable: exempt from env_prefix — already exact
	}
	return envVarFor(e.Name, envPrefix)
}

// envVarFor derives the environment variable a recon key binds to under envPrefix — the ONE
// place that derivation happens.
//
// It used to happen in three: this function's old body, [envVarLabel] for the help page, and
// recon's own SnakeUpperTransform inside the input reader. They disagreed, and a reader had no way
// to tell. An input named "base_url" under env_prefix MUSAK was PRINTED in help as
// MUSAK_BASE_URL and BOUND from nothing at all, because recon's inverse projection splits on
// every underscore and so read MUSAK_BASE_URL back as the two-segment path base/url, which
// never met the one-segment key. "apiKey" had the same disagreement the other way: help said
// API_KEY, the input reader read APIKEY.
//
// The cure is not a fourth spelling. It is emitting the name this function returns into the
// generated field's `env:` tag, so the input reader PINS it — exempt from any prefix, both
// directions — instead of re-deriving it. Help and the input reader then read one fact.
func envVarFor(key, envPrefix string) string {
	derived := snakeUpper(strings.ReplaceAll(key, ".", "_"))
	if envPrefix != "" {
		return envPrefix + "_" + derived
	}
	return derived
}
