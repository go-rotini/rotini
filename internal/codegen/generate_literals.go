package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Go-literal SOURCE emission: the Definition / BindMeta / *Def builders the generated
// framework file embeds. The typed-input field derivation that feeds these (and the
// shared eachConstraint enumerator) lives in inputs_fields.go.

// literal fields an Inputs contributes to a Definition or CommandDef literal,
// omitting any that render empty. Shared by renderDefinition (the root) and
// rnodesLiteral (each command node) so the field set is enumerated once.
func writeInputDefsLiteral(b *strings.Builder, in *Inputs) {
	if fl := flagDefsLiteral(in); fl != "" {
		b.WriteString("Flags: " + fl + ",\n")
	}
	if al := argDefsLiteral(in); al != "" {
		b.WriteString("Arguments: " + al + ",\n")
	}
	if fg := flagGroupsLiteral(in); fg != "" {
		b.WriteString("FlagGroups: " + fg + ",\n")
	}
	if fd := flagDependenciesLiteral(in); fd != "" {
		b.WriteString("FlagDependencies: " + fd + ",\n")
	}
}

// renderDefinition renders the `var definition = rotini.Definition{…}` literal —
// the compiled command tree the runtime parses against. It is unexported: end-users
// hold the *Program (from the generated NewProgram), never the Definition. Emitted into
// the framework file and gofmt-formatted with the rest of it, so the produced text only
// needs to be valid Go, not pretty.
func renderDefinition(gp *program) string {
	var b strings.Builder
	b.WriteString("var definition = " + rotiniPkgName + ".Definition{\n")
	b.WriteString("Name: " + strconv.Quote(gp.rootName) + ",\n")
	b.WriteString("Handler: " + strconv.Quote(gp.rootPascal) + ",\n")
	if gp.rootPassthrough {
		b.WriteString("Passthrough: true,\n")
	}
	writeInputDefsLiteral(&b, gp.rootInputs)
	if cl := rnodesLiteral(gp.rootName, gp.tree); cl != "" {
		b.WriteString("Commands: " + cl + ",\n")
	}
	if rl := remoteDefsLiteral(gp.rootName, gp.rootRemotes); rl != "" {
		b.WriteString("RemoteCommands: " + rl + ",\n")
	}
	if dl := discoveryLiteral(gp.rootName, gp.rootDiscovery); dl != "" {
		b.WriteString("Discovery: " + dl + ",\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// renderBindMeta renders the `var BindMeta = rotini.BindMeta{…}` descriptor the
// default binder consumes — the document-level config-file sources. Returns "" when
// there are none (so a CLI with no configuration_files stays unchanged).
func renderBindMeta(gp *program) string {
	// Emitted UNCONDITIONALLY (ergonomics E3-S2): an empty descriptor is the
	// honest zero — handler and main.go code can reference BindMeta uniformly,
	// and the generated NewProgram binds it under rotini.KeyBindMeta either way.
	files := gp.configFiles
	stdinSchemas := collectStdinSchemas(gp)
	var b strings.Builder
	b.WriteString("// BindMeta is the generated descriptor the default binder (rotini.Binder) consumes.\n")
	b.WriteString("var BindMeta = " + rotiniPkgName + ".BindMeta{\n")
	if gp.envPrefix != "" {
		b.WriteString("EnvPrefix: " + strconv.Quote(gp.envPrefix) + ",\n")
	}
	if len(files) > 0 {
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
			if d := f.Discover; d != nil {
				b.WriteString(", Discover: &" + rotiniPkgName + ".DiscoverDef{Strategy: " + strconv.Quote(d.Strategy) + ", File: " + strconv.Quote(d.File))
				if d.App != "" {
					b.WriteString(", App: " + strconv.Quote(d.App))
				}
				b.WriteString("}")
			}
			if f.Schema != nil {
				if js := validationSchema(*f.Schema, gp.schemas); js != "" {
					b.WriteString(", Schema: " + goRawString(js))
				}
			}
			if c, ok := pathFrom[f.Name]; ok {
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
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
	}
	if len(stdinSchemas) > 0 {
		b.WriteString("StdinSchemas: map[string]string{\n")
		keys := make([]string, 0, len(stdinSchemas))
		for k := range stdinSchemas {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(strconv.Quote(k) + ": " + goRawString(stdinSchemas[k]) + ",\n")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

// goRawString renders s as a Go string literal, preferring a backtick raw string
// (clean for embedded JSON) and falling back to a quoted literal if s contains a
// backtick.
func goRawString(s string) string {
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// discoveryLiteral renders the *rotini.RemoteDiscoveryDef literal for a command's
// plugin discovery, or "" when discovery is off. The prefix defaults to "<host>-"
// (the root binary name) when the spec leaves it unset.
func discoveryLiteral(host string, d *RemoteDiscovery) string {
	if d == nil {
		return ""
	}
	prefix := d.Prefix
	if prefix == "" {
		prefix = host + "-"
	}
	var b strings.Builder
	b.WriteString("&" + rotiniPkgName + ".RemoteDiscoveryDef{Prefix: " + strconv.Quote(prefix))
	if d.Path != "" {
		b.WriteString(", Path: " + strconv.Quote(d.Path))
	}
	if d.Hidden {
		b.WriteString(", Hidden: true")
	}
	b.WriteString("}")
	return b.String()
}

// sliceLiteral renders a "[]rotini.<typeName>{ ... }" Go literal (one element per
// item), or "" when items is empty. renderItem writes one element's body — the
// text between the element's surrounding "{" and "}," which sliceLiteral supplies.
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

// remoteDefsLiteral renders the []rotini.RemoteDef literal for a command's
// remote/co-located sub-commands. The expected binary is "<host>-<name>".
func remoteDefsLiteral(host string, rcs []RemoteCommandSpec) string {
	return sliceLiteral("RemoteDef", rcs, func(b *strings.Builder, rc RemoteCommandSpec) {
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

func flagDefsLiteral(in *Inputs) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("FlagDef", in.Flags, func(b *strings.Builder, f FlagInput) {
		b.WriteString("Name: " + strconv.Quote(f.Name) + ", Identifiers: " + goStringSlice(flagIdentifiers(f)))
		if f.Summary != "" {
			b.WriteString(", Summary: " + strconv.Quote(f.Summary))
		}
		defType := getSchemaType(f.Schema)
		if f.Schema != nil && f.Schema.Type == "count" {
			defType = "count" // the parser needs the count semantics; the FIELD is int
		}
		b.WriteString(", Type: " + strconv.Quote(defType))
		writeSchemaCommon(b, f.Schema)
		if f.Hidden {
			b.WriteString(", Hidden: true")
		}
		if len(f.DeprecatedIdentifiers) > 0 {
			b.WriteString(", DeprecatedIdentifiers: " + goStringSlice(f.DeprecatedIdentifiers))
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
	})
}

// keyPaths flattens a map flag's declared properties into the key vocabulary
// shell completion offers before the '=': dotted paths through nested object
// properties when the flag opts into dotted_keys, top-level property names
// otherwise. Sorted, since properties is a map. Nil for non-map flags.
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

func argDefsLiteral(in *Inputs) string {
	if in == nil {
		return ""
	}
	return sliceLiteral("ArgDef", in.Arguments, func(b *strings.Builder, a ArgumentInput) {
		typ := getSchemaType(a.Schema)
		b.WriteString("Name: " + strconv.Quote(a.Name) + ", Type: " + strconv.Quote(typ))
		if strings.HasPrefix(typ, "[]") {
			b.WriteString(", Variadic: true")
		}
		writeSchemaCommon(b, a.Schema)
		if a.Hidden {
			b.WriteString(", Hidden: true")
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

// rnodesLiteral renders the []rotini.CommandDef literal for a resolved command
// tree (recursing into children), or "" when nodes is empty. host prefixes the
// remote binary names for any discovery nodes.
func rnodesLiteral(host string, nodes []rnode) string {
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
		writeInputDefsLiteral(b, n.inputs)
		if cl := rnodesLiteral(host, n.children); cl != "" {
			b.WriteString("Commands: " + cl + ",\n")
		}
		if rl := remoteDefsLiteral(host, n.remotes); rl != "" {
			b.WriteString("Remotes: " + rl + ",\n")
		}
		if dl := discoveryLiteral(host, n.discovery); dl != "" {
			b.WriteString("Discovery: " + dl + ",\n")
		}
	})
}

// writeSchemaCommon appends the Required/Default/Enum fields shared by FlagDef
// and ArgDef literals, omitting zero values.
func writeSchemaCommon(b *strings.Builder, schema *InputSchema) {
	if schema == nil {
		return
	}
	if schema.Required {
		b.WriteString(", Required: true")
	}
	if d := defaultString(schema.Default); d != "" {
		b.WriteString(", Default: " + strconv.Quote(d))
	}
	if len(schema.Enum) > 0 {
		b.WriteString(", Enum: " + goStringSlice(schema.Enum))
	}
	if schema.Secret {
		b.WriteString(", Secret: true")
	}
	if c := constraintsLiteral(schema); c != "" {
		b.WriteString(", Constraints: " + c)
	}
}

// constraintsLiteral renders a rotini.Constraints{…} literal from a schema's declared
// numeric/string/array bounds, or "" when none are set. The numeric bounds are
// presence-carrying: a declared bound (0 included) emits a rotini.Ptr literal;
// an undeclared one emits nothing. Length/count bounds keep the zero-sentinel
// convention.
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

// getSchemaType resolves an input schema to the Definition's type string,
// defaulting to "string". An array schema honors its `items:` element type
// ("array" + items int → "[]int"); without items it stays "[]string".
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
