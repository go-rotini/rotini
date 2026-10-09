package codegen

import (
	"encoding/json"
	"strconv"
	"strings"
)

// This file derives the generated struct fields from a command's Inputs and maps schema
// types to Go types. The literals those fields feed live in generate_literals.go.

// inputsFields returns the fields of a command's <Prefix>Inputs struct: one per ancestor plus
// the command itself, in root-to-leaf order, each named after that command's PascalCase prefix.
//
// The fields carry no struct tag. The input reader maps them to chain frames by position,
// counting back from the running frame; names are not matched because an umbrella may rename
// a composed child whose types were generated under the child's own names.
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

// flagFields returns the <Prefix>Flags struct fields for a command's argv flags. A flag
// with an env or config fallback still lives here; the input reader reconciles it.
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
			// The env fallback is pinned at generate time; see [envVarFor].
			EnvVar:  flagEnvVar(f.Schema, key, envPrefix),
			Comment: contractComment(f.Schema),
		})
		if key != "" && f.Schema.VariableFile != "" {
			fields[len(fields)-1].EnvFile = f.Schema.VariableFile
		}
	}
	return fields
}

// flagReconKey is a flag's (or an argument's) reconciliation key, which opts it into the fallback chain
// (argv > env > config > default); "" for an argv-only flag. An explicit `key:` is used
// as-is. `variable:` alone also opts in, keyed by the flag's name, so --token can read
// GITHUB_TOKEN without a config key.
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

// envFields returns the <Prefix>Env struct fields, one per environment input. The recon key
// is the input name, and the variable is always pinned in the field's tag (see envVarFor).
func envFields(in *Inputs, envPrefix string) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Env))
	for _, e := range in.Env {
		fd := fieldDef{
			Field: toPascalCase(e.Name), GoType: goFieldType(e.Schema), Tag: e.Name,
			Import: fieldImport(e.Schema), Recon: reconTag(e.Name, e.Schema),
			EnvVar:     envVarName(e, envPrefix),
			Constraint: constraintTags(e.Schema),
		}
		// recon splits an env list or map on its separator option (default ","), plainly.
		if e.Schema != nil && e.Schema.Separator != "" && e.Schema.Separator != "," {
			fd.Recon += ",separator=" + e.Schema.Separator
		}
		if e.Schema != nil && e.Schema.Nesting == "" {
			fd.EnvFile = e.Schema.VariableFile
		}
		// A nested input's variable is a family prefix, so it goes in the envnest tag
		// instead of env:, and rotini (not recon) enforces required, since recon
		// resolves leaf keys only. lintVariable allows a single name here.
		if e.Schema != nil && e.Schema.Nesting != "" {
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

// constraintTags renders an env/config input's constraints, layout, and enum as
// space-separated struct tags (e.g. `min:"1" max:"65535" pattern:"^x$"`) for the input reader
// to enforce, or "" when none are set. It mirrors the FlagDef/ArgDef constraints the parser
// enforces for argv.
//
// Values are written with strconv.Quote because reflect reads tag values back through
// strconv.Unquote; a raw `^\d+$` would be an invalid escape and the tag would read as absent.
// An enum is encoded as a JSON array so members may contain any character.
func constraintTags(schema *InputSchema) string {
	var parts []string
	eachConstraint(schema, func(tag, _, tagVal, _ string) {
		parts = append(parts, tag+":"+strconv.Quote(tagVal))
	})
	if l := layoutsFor(schema); len(l) > 0 {
		parts = append(parts, "layout:"+strconv.Quote(l[0]))
		if len(l) > 1 {
			if list, err := json.Marshal(l); err == nil { // a []string always marshals
				parts = append(parts, "layouts:"+strconv.Quote(string(list)))
			}
		}
	}
	if r := relative(schema); r != "" {
		parts = append(parts, "relative:"+strconv.Quote(r))
	}
	if schema != nil && len(schema.Enum) > 0 {
		if members, err := json.Marshal(enumStrings(schema.Enum)); err == nil { // a []string always marshals
			parts = append(parts, "enum:"+strconv.Quote(string(members)))
		}
		if schema.IgnoreCase {
			parts = append(parts, `ignorecase:"true"`)
		}
		if aliases := enumAliases(schema.Enum); aliases != nil {
			if m, err := json.Marshal(aliases); err == nil { // a map[string]string always marshals
				parts = append(parts, "enumalias:"+strconv.Quote(string(m)))
			}
		}
		if unlisted := enumUnlisted(schema.Enum); unlisted != nil {
			if m, err := json.Marshal(unlisted); err == nil {
				parts = append(parts, "enumunlisted:"+strconv.Quote(string(m)))
			}
		}
	}
	return strings.Join(parts, " ")
}

// eachConstraint visits every present validation constraint on schema in a stable order,
// passing its tag name, its Constraints field name, and its value rendered for a struct tag
// and for a Go literal. constraintTags and constraintsLiteral both use it so they cannot drift.
// Numeric bounds, maxLength and maxItems are present when non-nil, so a 0 is kept; minLength
// and minItems only when non-zero.
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
	intPtrC := func(tag, field string, p *int) {
		if p == nil {
			return
		}
		s := strconv.Itoa(*p)
		visit(tag, field, s, rotiniPkgName+".Ptr("+s+")")
	}
	floatC("min", "Minimum", bound(schema.Minimum))
	floatC("max", "Maximum", bound(schema.Maximum))
	floatC("xmin", "ExclusiveMinimum", bound(schema.ExclusiveMinimum))
	floatC("xmax", "ExclusiveMaximum", bound(schema.ExclusiveMaximum))
	floatC("multipleof", "MultipleOf", bound(schema.MultipleOf))
	intC("minlen", "MinLength", schema.MinLength)
	intPtrC("maxlen", "MaxLength", schema.MaxLength)
	intC("minitems", "MinItems", schema.MinItems)
	intPtrC("maxitems", "MaxItems", schema.MaxItems)
	if schema.Pattern != "" {
		visit("pattern", "Pattern", schema.Pattern, strconv.Quote(schema.Pattern))
		if schema.PatternMessage != "" {
			visit("patternmsg", "PatternMessage", schema.PatternMessage, strconv.Quote(schema.PatternMessage))
		}
	}
	if schema.UniqueItems {
		visit("unique", "UniqueItems", "true", "true")
	}
}

// envVarOf returns an input's explicit environment variables (schema.variable) comma-joined,
// or "" when it declares none. The input reader reads the first one that is set.
func envVarOf(schema *InputSchema) string {
	return strings.Join(variables(schema), ",")
}

// flagEnvVar is a flag's (or an argument's) pinned env-fallback variable: its explicit `variable:` (exempt from
// env_prefix), else the name derived from its recon key, else "" for an argv-only flag.
func flagEnvVar(schema *InputSchema, reconKey, envPrefix string) string {
	if v := envVarOf(schema); v != "" {
		return v
	}
	if reconKey == "" {
		return ""
	}
	return envVarFor(reconKey, envPrefix)
}

// configFields returns the <Prefix>Config struct fields, one per config-file input. The
// recon key is schema.key, else the input name.
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

// nestedReconTag is the recon tag body for a nested env input: the key and secret only.
// required and default are handled by the envnest fill, since the key is never a resolvable leaf.
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

// stdinTypeExpr returns the Go type for a command's Stdin field, or "" when the command
// declares no stdin: "*<Prefix>Stdin" for a document payload, "*string" for text, "*[]string"
// for lines, "*[]byte" for bytes and "*[]<Prefix>Stdin" for jsonl, where <Prefix>Stdin is one
// record. A pointer, so "nothing piped" differs from "empty payload". A streamed lines or jsonl
// stdin is an iterator instead, nil when stdin isn't read.
func stdinTypeExpr(prefix string, in *Inputs) string {
	if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
		return ""
	}
	stream := in.Stdin.Stream
	switch in.Stdin.Format {
	case "text":
		return "*string"
	case "bytes":
		return "*[]byte"
	case "lines":
		if stream {
			return "iter.Seq2[string, error]"
		}
		return "*[]string"
	case "jsonl":
		if stream {
			return "iter.Seq2[" + prefix + "Stdin, error]"
		}
		return "*[]" + prefix + "Stdin"
	}
	return "*" + prefix + "Stdin"
}

// rawStdinFormat reports whether a stdin format binds the payload directly rather than
// decoding it into a generated type.
func rawStdinFormat(format string) bool {
	return format == "text" || format == "lines" || format == "bytes"
}

// stdinFormatExpr returns the value of a command's
// `stdin:"<format>[,stream][,nul][,required][,unless=<argument>]"` struct tag: the decode
// format (default json), then the options the input reader needs: a streamed field, NUL as the
// line separator, an empty stdin rejected, and the file argument whose value means stdin is
// not read. "" when the command declares no stdin.
func stdinFormatExpr(in *Inputs) string {
	if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
		return ""
	}
	parts := []string{in.Stdin.Format}
	if parts[0] == "" {
		parts[0] = "json"
	}
	if in.Stdin.Stream {
		parts = append(parts, "stream")
	}
	if in.Stdin.Separator == "nul" {
		parts = append(parts, "nul")
	}
	if in.Stdin.Schema.Required {
		parts = append(parts, "required")
	}
	if in.Stdin.UnlessArgument != "" {
		parts = append(parts, "unless="+in.Stdin.UnlessArgument)
	}
	return strings.Join(parts, ",")
}

// argFields returns the <Prefix>Arguments struct fields for a command's inputs. An argument
// with an env or config fallback carries the same recon and env tags a flag does.
func argFields(in *Inputs, envPrefix string) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Arguments))
	for _, a := range in.Arguments {
		key := flagReconKey(a.Name, a.Schema)
		fields = append(fields, fieldDef{
			Field: toPascalCase(a.Name), GoType: goFieldType(a.Schema), Tag: a.Name, Import: fieldImport(a.Schema),
			Recon: key, EnvVar: flagEnvVar(a.Schema, key, envPrefix), Comment: contractComment(a.Schema),
		})
	}
	return fields
}

// goFieldType resolves an input schema to its Go field type: getSchemaType, wrapped in a
// pointer for nullable inputs.
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

// jsonSchemaTypeToGo maps a schema type name to a Go type expression. It accepts JSON
// Schema names, Go names, and rotini's aliases; unknown values pass through unchanged so
// custom types are usable. Go-style list and map spellings (`[]bytesize`,
// `map[string]duration`) resolve their element types recursively.
func jsonSchemaTypeToGo(t string) string {
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
		return "string" // a path; the declared type name makes the parser check it (see definitionType)
	case "inputfile", "outputfile":
		return "string" // a path, or "-" for stdin or stdout; checked like existingfile
	}
	if g, ok := valueTypeGo[t]; ok {
		return g
	}
	return t
}

// valueTypeGo is the Go type each of rotini's value types reads into.
var valueTypeGo = map[string]string{
	"duration":    "time.Duration",
	"time":        "time.Time",
	"datetime":    "time.Time",
	"date":        "time.Time",
	"url":         "*url.URL",
	"email":       "mail.Address",
	"timezone":    "*time.Location",
	"mac":         "net.HardwareAddr",
	"ip":          "netip.Addr",
	"cidr":        "netip.Prefix",
	"hostport":    "netip.AddrPort",
	"bytesize":    "rotini.ByteSize",
	"hexbytes":    "rotini.HexBytes",
	"base64bytes": "rotini.Base64Bytes",
	"regexp":      "*regexp.Regexp",
	"glob":        "rotini.Glob",
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

// rotiniTypeAliases are the non-Go type names jsonSchemaTypeToGo resolves. lintSchemaTypes
// uses it to tell an alias from a typo; TestRotiniTypeAliasesMatchTheResolver keeps it in
// step with the switch above.
var rotiniTypeAliases = []string{
	"boolean", "integer", "number", "array", "object", "map", "count",
	"existingfile", "existingdir", "duration", "time", "datetime", "date",
	"url", "email", "timezone", "mac", "ip", "cidr", "hostport",
	"bytesize", "hexbytes", "base64bytes",
	"regexp", "glob",
	"inputfile", "outputfile",
}

// toTemplateFields converts fieldDefs to template fields, assembling each struct-tag
// literal. A fieldDef with no Tag (the <Prefix>Inputs fields) gets no struct tag.
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

// collectPathFrom maps each configuration_files name to the own-tree inputs supplying its
// path: a flag by logical name, an env input by its variable. Validation guarantees at most
// one claim per channel and that the named entry exists.
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

// contractComment is the TextUnmarshaler note emitted on flag and argument fields whose type
// comes from an explicit spec `import:`. Env and config fields decode through recon and get none.
func contractComment(schema *InputSchema) string {
	if schema == nil || strings.TrimSpace(schema.Import) == "" {
		return ""
	}
	return "// parsed via its encoding.TextUnmarshaler (see the spec schema's `type` docs)"
}

// envVarName is an env input's environment variable: the explicit `variable:` (exempt from
// env_prefix), else the name [envVarFor] derives from its logical name.
func envVarName(e EnvInput, envPrefix string) string {
	if v := envVarOf(e.Schema); v != "" {
		return v
	}
	return envVarFor(e.Name, envPrefix)
}

// envVarFor derives the environment variable a recon key binds to under envPrefix
// (e.g. "base_url" under MUSAK → MUSAK_BASE_URL). It is the only place this derivation
// happens: the result is pinned in the generated field's `env:` tag, so help and the input
// reader use the same name instead of re-deriving it.
func envVarFor(key, envPrefix string) string {
	derived := snakeUpper(strings.ReplaceAll(key, ".", "_"))
	if envPrefix != "" {
		return envPrefix + "_" + derived
	}
	return derived
}
