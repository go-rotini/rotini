package codegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file emits the Go source literals in the generated cmd file: the Definition,
// InputSettings, and their *Def elements. Field derivation lives in generate_inputs.go.

// writeInputDefsLiteral writes the fields an Inputs contributes to a Definition or
// CommandDef literal, omitting empty ones. renderDefinition and rnodesLiteral share it.
func writeInputDefsLiteral(b *strings.Builder, in *Inputs, schemas map[string]Schema) {
	if fl := flagDefsLiteral(in, schemas); fl != "" {
		b.WriteString("Flags: " + fl + ",\n")
	}
	if al := argDefsLiteral(in, schemas); al != "" {
		b.WriteString("Arguments: " + al + ",\n")
	}
	if fg := flagGroupsLiteral(in); fg != "" {
		b.WriteString("FlagGroups: " + fg + ",\n")
	}
	if fd := flagDependenciesLiteral(in); fd != "" {
		b.WriteString("FlagDependencies: " + fd + ",\n")
	}
}

// renderDefinition renders the unexported `var definition = rotini.Definition{…}` literal,
// the command tree the runtime parses against. The output is gofmt'd with the rest of the
// file, so it need only be valid Go.
func renderDefinition(gp *program) string {
	var b strings.Builder
	b.WriteString("var definition = " + rotiniPkgName + ".Definition{\n")
	b.WriteString("Name: " + strconv.Quote(gp.rootName) + ",\n")
	b.WriteString("Handler: " + strconv.Quote(gp.rootPascal) + ",\n")
	if gp.rootPassthrough {
		b.WriteString("Passthrough: true,\n")
	}
	writeInputDefsLiteral(&b, gp.rootInputs, gp.schemas)
	b.WriteString(outputDefLiteral(gp.rootPascal+"Output", gp.rootOutput, gp.schemas))
	if cl := rnodesLiteral(gp.rootName, gp.tree, gp.schemas); cl != "" {
		b.WriteString("Commands: " + cl + ",\n")
	}
	if rl := pluginDefsLiteral(gp.rootName, gp.rootPlugins); rl != "" {
		b.WriteString("Plugins: " + rl + ",\n")
	}
	if dl := discoveryLiteral(gp.rootName, gp.rootDiscovery); dl != "" {
		b.WriteString("PluginDiscovery: " + dl + ",\n")
	}
	if gp.rootPluginPath != "" {
		b.WriteString("PluginPath: " + strconv.Quote(gp.rootPluginPath) + ",\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// renderInputSettings renders the `var InputSettings = rotini.InputSettings{…}` descriptor
// the default input reader consumes: the env prefix, config-file sources, and stdin schemas.
// It is always emitted, even when empty, because NewProgram references it unconditionally.
func renderInputSettings(gp *program) string {
	files := gp.configFiles
	stdinSchemas := collectStdinSchemas(gp)
	var b strings.Builder
	b.WriteString("// InputSettings is the generated descriptor the default input reader (rotini.InputReader) reads.\n")
	b.WriteString("var InputSettings = " + rotiniPkgName + ".InputSettings{\n")
	if gp.envPrefix != "" {
		b.WriteString("EnvPrefix: " + strconv.Quote(gp.envPrefix) + ",\n")
	}
	renderConfigFiles(&b, gp, files)
	renderStdinSchemas(&b, stdinSchemas)
	b.WriteString("}")
	return b.String()
}

// renderConfigFiles renders the InputSettings literal's ConfigFiles field, one
// rotini.ConfigFile per scoped configuration_files entry, or nothing when there are none.
func renderConfigFiles(b *strings.Builder, gp *program, files []scopedConfigFile) {
	if len(files) == 0 {
		return
	}
	pathFrom := collectPathFrom(gp)
	b.WriteString("ConfigFiles: []" + rotiniPkgName + ".ConfigFile{\n")
	for _, f := range files {
		b.WriteString("{Name: " + strconv.Quote(f.Name))
		b.WriteString(", Scope: " + strconv.Quote(f.Scope))
		if f.Path != "" {
			b.WriteString(", Path: " + strconv.Quote(f.Path))
		}
		if f.Format != "" {
			b.WriteString(", Format: " + strconv.Quote(f.Format))
		}
		renderDiscover(b, f.Discover)
		if f.Schema != nil {
			if js := validationSchema(*f.Schema, gp.schemas); js != "" {
				b.WriteString(", Schema: " + goRawString(js))
			}
		}
		if c, ok := pathFrom[f.Name]; ok {
			renderPathFrom(b, c)
		}
		b.WriteString("},\n")
	}
	b.WriteString("},\n")
}

// renderDiscover renders a config file's Discover field, or nothing when d is nil.
func renderDiscover(b *strings.Builder, d *ConfigurationFileDiscover) {
	if d == nil {
		return
	}
	b.WriteString(", Discover: &" + rotiniPkgName + ".DiscoverDef{Strategy: " + strconv.Quote(d.Strategy) + ", File: " + strconv.Quote(d.File))
	if d.App != "" {
		b.WriteString(", App: " + strconv.Quote(d.App))
	}
	b.WriteString("}")
}

// renderPathFrom renders a config file's PathFrom field: the flag and/or env var whose
// value supplies the file's path at run time.
func renderPathFrom(b *strings.Builder, c pathFromClaim) {
	b.WriteString(", PathFrom: &" + rotiniPkgName + ".PathFromDef{")
	if c.flag != "" {
		b.WriteString("Flag: " + strconv.Quote(c.flag))
		if c.env != "" {
			b.WriteString(", ")
		}
	}
	if c.env != "" {
		b.WriteString("Env: " + strconv.Quote(c.env))
	}
	b.WriteString("}")
}

// renderStdinSchemas renders the InputSettings literal's StdinSchemas field: each command's
// stdin validation schema keyed by command path, in sorted order.
func renderStdinSchemas(b *strings.Builder, schemas map[string]string) {
	if len(schemas) == 0 {
		return
	}
	b.WriteString("StdinSchemas: map[string]string{\n")
	keys := make([]string, 0, len(schemas))
	for k := range schemas {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(strconv.Quote(k) + ": " + goRawString(schemas[k]) + ",\n")
	}
	b.WriteString("},\n")
}

// goRawString renders s as a backtick raw string literal, or a quoted literal when s
// contains a backtick.
func goRawString(s string) string {
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// discoveryLiteral renders the *rotini.PluginDiscoveryDef literal for a command's plugin
// discovery, or "" when discovery is off. The prefix defaults to "<host>-".
func discoveryLiteral(host string, d *PluginDiscovery) string {
	if d == nil {
		return ""
	}
	prefix := d.Prefix
	if prefix == "" {
		prefix = host + "-"
	}
	var b strings.Builder
	b.WriteString("&" + rotiniPkgName + ".PluginDiscoveryDef{Prefix: " + strconv.Quote(prefix))
	if d.Hidden {
		b.WriteString(", Hidden: true")
	}
	b.WriteString("}")
	return b.String()
}

// sliceLiteral renders a "[]rotini.<typeName>{…}" literal with one element per item, or ""
// when items is empty. renderItem writes each element's body; sliceLiteral adds the braces.
func sliceLiteral[T any](typeName string, items []T, renderItem func(b *strings.Builder, item T)) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[]" + rotiniPkgName + "." + typeName + "{\n")
	for _, it := range items {
		b.WriteString("{")
		renderItem(&b, it)
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

// pluginDefsLiteral renders the []rotini.PluginDef literal for a command's declared
// plugins. Each binary is "<host>-<name>"; an invalid or non-positive timeout is omitted.
func pluginDefsLiteral(host string, rcs []PluginSpec) string {
	return sliceLiteral("PluginDef", rcs, func(b *strings.Builder, rc PluginSpec) {
		b.WriteString("Name: " + strconv.Quote(rc.Name))
		if rc.Summary != "" {
			b.WriteString(", Summary: " + strconv.Quote(rc.Summary))
		}
		b.WriteString(", Binary: " + strconv.Quote(host+"-"+rc.Name))
		if len(rc.Aliases) > 0 {
			b.WriteString(", Aliases: " + goStringSlice(rc.Aliases))
		}
		if rc.Timeout != "" {
			if d, err := time.ParseDuration(rc.Timeout); err == nil && d > 0 {
				fmt.Fprintf(b, ", Timeout: %d", int64(d))
			}
		}
	})
}

// flagDefsLiteral renders the []rotini.FlagDef literal for a command's flags, or "" when
// there are none.
func flagDefsLiteral(in *Inputs, schemas map[string]Schema) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("FlagDef", in.Flags, func(b *strings.Builder, f FlagInput) {
		b.WriteString("Name: " + strconv.Quote(f.Name) + ", Identifiers: " + goStringSlice(flagIdentifiers(f)))
		if f.Summary != "" {
			b.WriteString(", Summary: " + strconv.Quote(f.Summary))
		}
		b.WriteString(", Type: " + strconv.Quote(definitionType(f.Schema, schemas)))
		objectSchema := objectSchemaFor(f.Schema, schemas)
		if objectSchema != "" {
			writeSchemaCommon(b, withObjectDefault(f.Schema))
			b.WriteString(", ObjectSchema: " + goRawString(objectSchema))
		} else {
			writeSchemaCommon(b, f.Schema)
		}
		if f.Hidden {
			b.WriteString(", Hidden: true")
		}
		if len(f.DeprecatedIdentifiers) > 0 {
			b.WriteString(", DeprecatedIdentifiers: " + goStringSlice(f.DeprecatedIdentifiers))
		}
		if f.Deprecated != "" {
			b.WriteString(", Deprecated: " + strconv.Quote(f.Deprecated))
		}
		if f.Schema != nil && f.Schema.Negatable {
			b.WriteString(", Negatable: true")
		}
		if f.Schema != nil && f.Schema.ImplicitValue != nil {
			b.WriteString(", ImplicitValue: " + strconv.Quote(defaultString(f.Schema.ImplicitValue)))
		}
		if f.Schema != nil && f.Schema.DottedKeys {
			b.WriteString(", DottedKeys: true")
		}
		if kp := keyPaths(f.Schema); len(kp) > 0 {
			b.WriteString(", KeyPaths: " + goStringSlice(kp))
		}
		if f.Schema != nil && len(f.Schema.From) > 0 {
			b.WriteString(", From: " + goStringSlice(f.Schema.From))
		}
		b.WriteString(completionLiteral(f.Schema))
	})
}

// layoutFor is the layout a time input is parsed under: its declared `layout:`, else
// "2006-01-02" for `type: date` (or a list of dates), else "" (RFC 3339).
func layoutFor(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if schema.Layout != "" {
		return schema.Layout
	}
	t := schema.Type
	if schema.Items != nil && schema.Items.Type != "" && jsonSchemaTypeToGo(t) == "[]string" {
		t = schema.Items.Type
	}
	if strings.TrimPrefix(t, "[]") == "date" {
		return "2006-01-02"
	}
	return ""
}

// objectRef returns the $ref of an object-valued input (its own, or its items' for a list),
// or "" when the input does not refer to an object schema.
func objectRef(schema *InputSchema, schemas map[string]Schema) string {
	if schema == nil {
		return ""
	}
	ref := schema.Ref
	if ref == "" && schema.Items != nil && strings.HasPrefix(getSchemaType(schema), "[]") {
		ref = schema.Items.Ref
	}
	named, ok := schemas[refTypeName(ref)]
	if !ok || (named.Type != "object" && len(named.Properties) == 0) {
		return ""
	}
	return ref
}

// objectSchemaFor renders the self-contained JSON Schema an object flag's value is validated
// against, or "" for any other flag.
func objectSchemaFor(schema *InputSchema, schemas map[string]Schema) string {
	ref := objectRef(schema, schemas)
	if ref == "" {
		return ""
	}
	return validationSchema(Schema{Ref: ref}, schemas)
}

// withObjectDefault returns schema with an object flag's default re-encoded as JSON (one
// document for an object, one per element for a list), the form the flag decodes, instead
// of the generic key=value rendering.
func withObjectDefault(schema *InputSchema) *InputSchema {
	if schema == nil || schema.Default == nil {
		return schema
	}
	out := *schema
	switch d := schema.Default.(type) {
	case map[string]any:
		if raw, err := json.Marshal(d); err == nil {
			out.Default = string(raw)
		}
	case []any:
		docs := make([]any, 0, len(d))
		for _, e := range d {
			raw, err := json.Marshal(e)
			if err != nil {
				return schema
			}
			docs = append(docs, string(raw))
		}
		out.Default = docs
	}
	return &out
}

// completionLiteral renders an input's `complete:` hint as the Completion field of its
// generated FlagDef/ArgDef literal, or "" when it declares none.
func completionLiteral(schema *InputSchema) string {
	if schema == nil || schema.Complete == nil || schema.Complete.Kind == "" {
		return ""
	}
	out := ", Complete: " + rotiniPkgName + ".Completion{Kind: " + strconv.Quote(schema.Complete.Kind)
	if len(schema.Complete.Extensions) > 0 {
		out += ", Extensions: " + goStringSlice(schema.Complete.Extensions)
	}
	return out + "}"
}

// keyPaths lists the sorted keys shell completion offers before a map flag's '=': dotted
// paths through nested properties when dotted_keys is set, else top-level property names.
// It returns nil for non-map flags.
func keyPaths(schema *InputSchema) []string {
	if schema == nil || !strings.HasPrefix(getSchemaType(schema), "map[") || len(schema.Properties) == 0 {
		return nil
	}
	var out []string
	var walk func(prefix string, props map[string]Schema)
	walk = func(prefix string, props map[string]Schema) {
		for name, p := range props {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if schema.DottedKeys && len(p.Properties) > 0 {
				walk(path, p.Properties)
				continue
			}
			out = append(out, path)
		}
	}
	walk("", schema.Properties)
	sort.Strings(out)
	return out
}

// argDefsLiteral renders the []rotini.ArgDef literal for a command's arguments, or "" when
// there are none. A list-typed argument is variadic.
func argDefsLiteral(in *Inputs, schemas map[string]Schema) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("ArgDef", in.Arguments, func(b *strings.Builder, a ArgumentInput) {
		typ := definitionType(a.Schema, schemas)
		b.WriteString("Name: " + strconv.Quote(a.Name) + ", Type: " + strconv.Quote(typ))
		if strings.HasPrefix(typ, "[]") {
			b.WriteString(", Variadic: true")
		}
		writeSchemaCommon(b, a.Schema)
		b.WriteString(completionLiteral(a.Schema))
		if a.Hidden {
			b.WriteString(", Hidden: true")
		}
		if a.Deprecated != "" {
			b.WriteString(", Deprecated: " + strconv.Quote(a.Deprecated))
		}
	})
}

// flagGroupsLiteral renders the []rotini.FlagGroup literal for a command's flag
// groups, or "" when none are declared.
func flagGroupsLiteral(in *Inputs) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("FlagGroup", in.FlagGroups, func(b *strings.Builder, g FlagGroup) {
		b.WriteString("Kind: " + strconv.Quote(g.Kind) + ", Flags: " + goStringSlice(g.Flags))
	})
}

// flagDependenciesLiteral renders the []rotini.FlagDependency literal for a command's
// conditional cross-flag requirements, or "" when none are declared.
func flagDependenciesLiteral(in *Inputs) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("FlagDependency", in.FlagDependencies, func(b *strings.Builder, d FlagDependency) {
		b.WriteString("When: " + strconv.Quote(d.When) + ", Requires: " + goStringSlice(d.Requires))
	})
}

// rnodesLiteral renders the []rotini.CommandDef literal for a resolved command tree,
// recursively, or "" when nodes is empty. host prefixes plugin binary names unless a node
// carries its own pluginHost.
func rnodesLiteral(host string, nodes []rnode, schemas map[string]Schema) string {
	return sliceLiteral("CommandDef", nodes, func(b *strings.Builder, n rnode) {
		b.WriteString("Name: " + strconv.Quote(n.name) + ",\n")
		b.WriteString("Handler: " + strconv.Quote(n.prefix) + ",\n")
		if n.help.Summary != "" {
			b.WriteString("Summary: " + strconv.Quote(n.help.Summary) + ",\n")
		}
		if n.hidden {
			b.WriteString("Hidden: true,\n")
		}
		if n.passthrough {
			b.WriteString("Passthrough: true,\n")
		}
		if len(n.aliases) > 0 {
			b.WriteString("Aliases: " + goStringSlice(n.aliases) + ",\n")
		}
		if len(n.deprecatedIdentifiers) > 0 {
			b.WriteString("DeprecatedIdentifiers: " + goStringSlice(n.deprecatedIdentifiers) + ",\n")
		}
		if n.deprecated != "" {
			b.WriteString("Deprecated: " + strconv.Quote(n.deprecated) + ",\n")
		}
		writeInputDefsLiteral(b, n.inputs, schemas)
		if !n.composed { // a composed command's output type lives in its own cli's package
			b.WriteString(outputDefLiteral(n.prefix+"Output", n.output, schemas))
		}
		if cl := rnodesLiteral(host, n.children, schemas); cl != "" {
			b.WriteString("Commands: " + cl + ",\n")
		}
		pluginHost := host
		if n.pluginHost != "" {
			pluginHost = n.pluginHost
		}
		if rl := pluginDefsLiteral(pluginHost, n.plugins); rl != "" {
			b.WriteString("Plugins: " + rl + ",\n")
		}
		if dl := discoveryLiteral(pluginHost, n.discovery); dl != "" {
			b.WriteString("PluginDiscovery: " + dl + ",\n")
		}
		if n.pluginPath != "" {
			b.WriteString("PluginPath: " + strconv.Quote(n.pluginPath) + ",\n")
		}
	})
}

// writeSchemaCommon appends the schema fields shared by FlagDef and ArgDef literals
// (Required, Default(s), Enum, Secret, Separator, Layout, Constraints), omitting zero values.
func writeSchemaCommon(b *strings.Builder, schema *InputSchema) {
	if schema == nil {
		return
	}
	if schema.Required {
		b.WriteString(", Required: true")
	}
	// A list or map default emits Defaults (one seeded occurrence each); anything else
	// emits the single Default.
	if list := defaultList(schema.Default); len(list) > 0 {
		b.WriteString(", Defaults: " + goStringSlice(list))
	} else if d := defaultString(schema.Default); d != "" {
		b.WriteString(", Default: " + strconv.Quote(d))
	}
	if len(schema.Enum) > 0 {
		b.WriteString(", Enum: " + goStringSlice(schema.Enum))
		if schema.IgnoreCase {
			b.WriteString(", IgnoreCase: true")
		}
	}
	if schema.Secret {
		b.WriteString(", Secret: true")
	}
	if schema.Separator != "" {
		b.WriteString(", Separator: " + strconv.Quote(schema.Separator))
	}
	if l := layoutFor(schema); l != "" {
		b.WriteString(", Layout: " + strconv.Quote(l))
	}
	if c := constraintsLiteral(schema); c != "" {
		b.WriteString(", Constraints: " + c)
	}
}

// constraintsLiteral renders a rotini.Constraints{…} literal from a schema's declared bounds,
// or "" when none are set. Numeric bounds are emitted as rotini.Ptr literals, so a declared 0
// is kept; length and count bounds treat 0 as unset.
func constraintsLiteral(schema *InputSchema) string {
	var parts []string
	eachConstraint(schema, func(_, field, _, litVal string) {
		parts = append(parts, field+": "+litVal)
	})
	if len(parts) == 0 {
		return ""
	}
	return rotiniPkgName + ".Constraints{" + strings.Join(parts, ", ") + "}"
}

// definitionType resolves an input schema to the type string on the emitted FlagDef/ArgDef,
// which can differ from the field's Go type (goFieldType):
//
//   - Parser-significant names (`count`, `existingfile`, `existingdir`, and lists of them)
//     are kept as declared, since their Go types (int, string) would erase the parse-time
//     behavior.
//   - A named scalar schema (`schemas: {Kind: {type: string}}`) resolves to its underlying
//     type, since the parser dispatches its checks on the type string.
//
// schemas may be nil.
func definitionType(schema *InputSchema, schemas map[string]Schema) string {
	if schema == nil {
		return getSchemaType(schema)
	}
	if t := namedScalarType(getSchemaType(schema), schemas); t != "" {
		return t
	}
	if schema.Ref != "" {
		return getSchemaType(schema)
	}
	if parserSignificantType(schema.Type) {
		return schema.Type
	}
	// Go-style list spelling: `[]existingfile`.
	if elem, ok := strings.CutPrefix(schema.Type, "[]"); ok && parserSignificantType(elem) {
		return schema.Type
	}
	// JSON Schema spelling: `type: array` with parser-significant `items`.
	if t := getSchemaType(schema); strings.HasPrefix(t, "[]") && schema.Items != nil &&
		schema.Items.Ref == "" && parserSignificantType(schema.Items.Type) {
		return "[]" + schema.Items.Type
	}
	return getSchemaType(schema)
}

// namedScalarType resolves typ (or a list's element) that names a non-object schema to the
// type that schema declares: "Kind" → "string", "[]Kind" → "[]string". It returns "" when
// typ names no such schema.
func namedScalarType(typ string, schemas map[string]Schema) string {
	elem, list := strings.CutPrefix(typ, "[]")
	src, ok := schemas[elem]
	if !ok || src.Type == "" || src.Type == "object" || len(src.Properties) > 0 {
		return ""
	}
	resolved := src.Type
	if !parserSignificantType(resolved) {
		resolved = jsonSchemaTypeToGo(resolved)
	}
	if list {
		return "[]" + resolved
	}
	return resolved
}

// parserSignificantType reports whether a declared type name has parser behavior its Go type
// would erase. Keep in sync with the parser's isPathType and count handling;
// TestDefinitionTypePreservesParserSemantics pins this set.
func parserSignificantType(t string) bool {
	return t == "count" || t == "existingfile" || t == "existingdir"
}

// getSchemaType resolves an input schema to its Go type expression, defaulting to "string".
// A $ref yields the named type; an array uses its `items:` element type ("array" + items int
// → "[]int"), else "[]string".
func getSchemaType(schema *InputSchema) string {
	if schema != nil {
		if name := refTypeName(schema.Ref); name != "" {
			return name
		}
		if schema.Type != "" {
			t := jsonSchemaTypeToGo(schema.Type)
			if t == "[]string" && schema.Items != nil {
				return "[]" + itemGoType(schema.Items)
			}
			return t
		}
	}
	return "string"
}

// itemGoType resolves an array schema's items to the element Go type,
// defaulting to "string".
func itemGoType(items *Schema) string {
	if name := refTypeName(items.Ref); name != "" {
		return name
	}
	if items.Type != "" {
		return jsonSchemaTypeToGo(items.Type)
	}
	return "string"
}

// goStringSlice renders a []string{…} literal.
func goStringSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// defaultList renders a multi-value default as the argv occurrences it seeds: one per list
// element, or one `key=value` per map entry; nil when the default is a scalar.
func defaultList(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			out = append(out, defaultString(it))
		}
		return out
	case map[string]any:
		// Sorted by key so the generated literal is byte-stable across runs.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, k+"="+defaultString(x[k]))
		}
		return out
	default:
		return nil
	}
}

// defaultString renders an input's decoded default value as a string.
func defaultString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprintf("%v", x)
	}
}
