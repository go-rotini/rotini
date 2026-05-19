package internal

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/go-rotini/rotini/rtk"
)

// RenderInput is the data context every template in the [templatesFS]
// renders against. The renderer composes it from the user's [rtk.ProgramSpec]
// (produced by [ToProgramSpec]) plus per-render options (package name,
// import paths). A single value drives every template so cross-template
// references resolve to identical labels.
type RenderInput struct {
	// Package is the Go package name the rendered file declares
	// (e.g., "rotini"). Used in the package clause.
	Package string

	// Spec is the resolved program spec. Templates walk it to emit
	// per-command types, handlers, the spec literal, etc.
	Spec rtk.ProgramSpec

	// FlatCommands is the spec's command tree flattened to a {path → *CommandSpec}
	// map. Path is hyphen-joined ("foo-bar-baz"); root is omitted. Sorted
	// iteration via [SortedCommandPaths].
	FlatCommands map[string]*rtk.CommandSpec
}

// NewRenderInput builds a [RenderInput] from spec + package name. The
// FlatCommands map is computed once so templates don't redo the walk.
func NewRenderInput(pkg string, spec rtk.ProgramSpec) RenderInput {
	return RenderInput{
		Package:      pkg,
		Spec:         spec,
		FlatCommands: flattenCommandSpecs(spec.Commands, ""),
	}
}

// SortedCommandPaths returns the keys of [RenderInput.FlatCommands] in
// deterministic (lexical) order so codegen output is stable across
// runs. Defined as a value-receiver method so Go templates can call it
// on the data context they receive.
func (r RenderInput) SortedCommandPaths() []string {
	keys := make([]string, 0, len(r.FlatCommands))
	for k := range r.FlatCommands {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Render renders the named template against the supplied input and runs
// gofmt over the result. The returned bytes are ready to write to disk
// or run through a comparison.
//
// Template names match the filenames under internal/templates (e.g.,
// "spec.gen.tmpl", "inputs.gen.tmpl"). Names that don't exist return an
// error.
func Render(name string, in RenderInput) ([]byte, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("internal: load templates: %w", err)
	}
	t := tmpl.Lookup(name)
	if t == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTemplate, name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("internal: execute %s: %w", name, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Surface both the format error and the unformatted source so
		// callers (esp. tests) can see what the template emitted.
		return buf.Bytes(), fmt.Errorf("internal: gofmt %s: %w", name, err)
	}
	return formatted, nil
}

// ErrUnknownTemplate is returned by [Render] when the requested
// template name isn't in the embedded set.
var ErrUnknownTemplate = sentinelError("unknown template")

// sentinelError implements error for the sentinel-value pattern used
// throughout this package.
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// loadTemplates parses every *.tmpl file in [templatesFS]. Templates can
// reference each other (e.g., via `{{template "name" .}}`) without
// explicit imports because they share one ParseFS tree.
func loadTemplates() (*template.Template, error) {
	root := template.New("rotini").Funcs(templateFuncs())
	t, err := root.ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return t, nil
}

// templateFuncs returns the function map every template gets access to.
// Helpers are pure, side-effect-free Go funcs operating on the
// [rtk.ProgramSpec] / [rtk.CommandSpec] data the renderer hands in.
//
// Naming convention: helpers that emit Go code use a "render" prefix;
// pure data transforms use verb-ish names matching their stdlib analog
// where one exists (e.g., "sortedKeys").
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// String / path helpers.
		"toPascalCase":                     toPascalCase,
		"lastPathSegment":                  lastPathSegment,
		"commandIntermediateAncestorPaths": commandIntermediateAncestorPaths,
		"goStringSlice":                    goStringSliceLiteral,
		"quote":                            quote,

		// Type helpers.
		"isPrimitiveType": isPrimitiveType,
		"isSliceType":     isSliceType,
		"isMapType":       isMapType,
		"goType":          goType,

		// Sort / map helpers.
		"sortedKeys":             sortedStringKeys,
		"sortedCommandSpecPaths": sortedCommandSpecPaths,

		// Spec-literal renderers.
		"renderProgramSpecLiteral": renderProgramSpecLiteral,
		"renderFlagSpecLiteral":    renderFlagSpecLiteralIndent,
		"renderArgSpecLiteral":     renderArgSpecLiteralIndent,
	}
}

// toPascalCase converts a string in kebab/snake/spaced form to
// PascalCase: "foo-bar_baz quux" → "FooBarBazQuux". Empty input yields
// empty output.
func toPascalCase(s string) string {
	if s == "" {
		return ""
	}
	var out []rune
	upper := true
	for _, r := range s {
		switch r {
		case '-', '_', ' ':
			upper = true
			continue
		}
		if upper {
			out = append(out, unicode.ToUpper(r))
			upper = false
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// lastPathSegment returns the trailing segment of a hyphen-joined
// command path. "foo-bar-baz" → "baz"; "add" → "add"; "" → "".
func lastPathSegment(path string) string {
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "-"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// commandIntermediateAncestorPaths returns every strict ancestor path
// of a hyphen-joined command path, root-first. "foo-bar-baz" → ["foo",
// "foo-bar"]. "foo" → []. "" → [].
//
// Used by the inputs/handlers templates to compose ancestor-scoped
// types ("foo.bar" inputs nested inside "foo-bar-baz" inputs).
func commandIntermediateAncestorPaths(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "-")
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "-"))
	}
	return out
}

// goStringSliceLiteral renders a []string as a compile-time Go literal:
// ["a", "b"] → `[]string{"a", "b"}`; empty slice → `nil`.
func goStringSliceLiteral(ss []string) string {
	if len(ss) == 0 {
		return "nil"
	}
	var b strings.Builder
	b.WriteString(`[]string{`)
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(s))
	}
	b.WriteString(`}`)
	return b.String()
}

// quote wraps s in Go double-quote form using strconv-style escaping.
// Used everywhere the templates emit a string literal.
func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

// isPrimitiveType reports whether t names one of the parser's built-in
// primitive types (string, int*, uint*, float*, bool). Slice/map/aliased
// types are NOT primitive.
func isPrimitiveType(t string) bool {
	switch t {
	case "string", "int", "int32", "int64", "uint", "uint32", "uint64",
		"float32", "float64", "bool":
		return true
	}
	return false
}

// isSliceType reports whether t is a Go slice type name ("[]string",
// "[]int", etc.).
func isSliceType(t string) bool {
	return strings.HasPrefix(t, "[]")
}

// isMapType reports whether t is a Go map type name ("map[K]V" or the
// bare alias "map").
func isMapType(t string) bool {
	return strings.HasPrefix(t, "map[") || t == "map"
}

// goType returns the Go-type expression for a type name. Currently a
// pass-through; reserved for alias expansions (e.g., "duration" →
// "time.Duration") that the ToProgramSpec translator now handles
// up-front, so callers see Go-form types here.
func goType(t string) string { return t }

// sortedStringKeys returns the lexically-sorted keys of any
// `map[string]V`. Used by templates iterating a map deterministically.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedCommandSpecPaths is the [rtk.CommandSpec]-typed counterpart
// of [sortedStringKeys] used by templates iterating
// [RenderInput.FlatCommands].
func sortedCommandSpecPaths(m map[string]*rtk.CommandSpec) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// flattenCommandSpecs walks the command tree producing a {hyphen-path →
// *CommandSpec} map. Root command (Path "") is omitted; the map only
// holds named sub-commands.
func flattenCommandSpecs(cmds []rtk.CommandSpec, parentPath string) map[string]*rtk.CommandSpec {
	out := make(map[string]*rtk.CommandSpec)
	for i := range cmds {
		c := &cmds[i]
		path := c.Name
		if parentPath != "" {
			path = parentPath + "-" + c.Name
		}
		out[path] = c
		maps.Copy(out, flattenCommandSpecs(c.Commands, path))
	}
	return out
}

// renderProgramSpecLiteral renders a complete rtk.ProgramSpec literal.
// The returned string is the body that follows `var Spec = `; it
// includes the outer braces but no trailing semicolon.
//
// Indent is the base indent (typically a single tab). Nested levels
// add one tab per level.
func renderProgramSpecLiteral(spec rtk.ProgramSpec, indent string) string {
	var b strings.Builder
	b.WriteString("rtk.ProgramSpec{\n")
	if spec.Name != "" {
		fmt.Fprintf(&b, "%s\tName: %q,\n", indent, spec.Name)
	}
	if len(spec.Flags) > 0 {
		b.WriteString(indent + "\tFlags: []rtk.FlagSpec{\n")
		for i := range spec.Flags {
			b.WriteString(renderFlagSpecLiteral(&spec.Flags[i], indent+"\t\t"))
		}
		b.WriteString(indent + "\t},\n")
	}
	if len(spec.Commands) > 0 {
		b.WriteString(indent + "\tCommands: []rtk.CommandSpec{\n")
		for i := range spec.Commands {
			b.WriteString(renderCommandSpecLiteral(&spec.Commands[i], indent+"\t\t"))
		}
		b.WriteString(indent + "\t},\n")
	}
	if spec.RootStdin != nil {
		b.WriteString(indent + "\tRootStdin: " + renderStdinSpecLiteral(spec.RootStdin) + ",\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

// renderCommandSpecLiteral renders a single CommandSpec entry suitable
// for inclusion in a parent slice. indent applies to the outer `{`.
func renderCommandSpecLiteral(cmd *rtk.CommandSpec, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "{\n")
	fmt.Fprintf(&b, "%s\tPath: %q,\n", indent, cmd.Path)
	fmt.Fprintf(&b, "%s\tName: %q,\n", indent, cmd.Name)
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(&b, "%s\tAliases: %s,\n", indent, goStringSliceLiteral(cmd.Aliases))
	}
	if cmd.Timeout != 0 {
		fmt.Fprintf(&b, "%s\tTimeout: %d, // %s\n", indent, int64(cmd.Timeout), cmd.Timeout.String())
	}
	if len(cmd.Flags) > 0 {
		b.WriteString(indent + "\tFlags: []rtk.FlagSpec{\n")
		for i := range cmd.Flags {
			b.WriteString(renderFlagSpecLiteral(&cmd.Flags[i], indent+"\t\t"))
		}
		b.WriteString(indent + "\t},\n")
	}
	if len(cmd.Arguments) > 0 {
		b.WriteString(indent + "\tArguments: []rtk.ArgumentSpec{\n")
		for i := range cmd.Arguments {
			b.WriteString(renderArgSpecLiteral(&cmd.Arguments[i], indent+"\t\t"))
		}
		b.WriteString(indent + "\t},\n")
	}
	if cmd.Stdin != nil {
		b.WriteString(indent + "\tStdin: " + renderStdinSpecLiteral(cmd.Stdin) + ",\n")
	}
	if len(cmd.Commands) > 0 {
		b.WriteString(indent + "\tCommands: []rtk.CommandSpec{\n")
		for i := range cmd.Commands {
			b.WriteString(renderCommandSpecLiteral(&cmd.Commands[i], indent+"\t\t"))
		}
		b.WriteString(indent + "\t},\n")
	}
	b.WriteString(indent + "},\n")
	return b.String()
}

// renderFlagSpecLiteral renders a single FlagSpec entry. indent applies
// to the outer `{`.
func renderFlagSpecLiteral(f *rtk.FlagSpec, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "{")
	fmt.Fprintf(&b, "Name: %q", f.Name)
	if len(f.Identifiers) > 0 {
		fmt.Fprintf(&b, ", Identifiers: %s", goStringSliceLiteral(f.Identifiers))
	}
	if f.Type != "" {
		fmt.Fprintf(&b, ", Type: %q", f.Type)
	}
	if f.Required {
		b.WriteString(", Required: true")
	}
	if f.Default != "" {
		fmt.Fprintf(&b, ", Default: %q", f.Default)
	}
	if len(f.Enum) > 0 {
		fmt.Fprintf(&b, ", Enum: %s", goStringSliceLiteral(f.Enum))
	}
	if f.Pattern != "" {
		fmt.Fprintf(&b, ", Pattern: %q", f.Pattern)
	}
	if f.Min != nil {
		fmt.Fprintf(&b, ", Min: rtk.Ptr(%g)", *f.Min)
	}
	if f.Max != nil {
		fmt.Fprintf(&b, ", Max: rtk.Ptr(%g)", *f.Max)
	}
	if f.MinLength != nil {
		fmt.Fprintf(&b, ", MinLength: rtk.Ptr(%d)", *f.MinLength)
	}
	if f.MaxLength != nil {
		fmt.Fprintf(&b, ", MaxLength: rtk.Ptr(%d)", *f.MaxLength)
	}
	if f.MinItems != nil {
		fmt.Fprintf(&b, ", MinItems: rtk.Ptr(%d)", *f.MinItems)
	}
	if f.MaxItems != nil {
		fmt.Fprintf(&b, ", MaxItems: rtk.Ptr(%d)", *f.MaxItems)
	}
	if f.Nullable {
		b.WriteString(", Nullable: true")
	}
	if f.EnvKey != "" {
		fmt.Fprintf(&b, ", EnvKey: %q", f.EnvKey)
	}
	if f.ConfigKey != "" {
		fmt.Fprintf(&b, ", ConfigKey: %q", f.ConfigKey)
	}
	if f.EnvOnly {
		b.WriteString(", EnvOnly: true")
	}
	b.WriteString("},\n")
	return b.String()
}

// renderArgSpecLiteral renders a single ArgumentSpec entry.
func renderArgSpecLiteral(a *rtk.ArgumentSpec, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "{")
	fmt.Fprintf(&b, "Name: %q", a.Name)
	if a.Type != "" {
		fmt.Fprintf(&b, ", Type: %q", a.Type)
	}
	if a.Required {
		b.WriteString(", Required: true")
	}
	if a.Variadic {
		b.WriteString(", Variadic: true")
	}
	if a.Default != "" {
		fmt.Fprintf(&b, ", Default: %q", a.Default)
	}
	if len(a.Enum) > 0 {
		fmt.Fprintf(&b, ", Enum: %s", goStringSliceLiteral(a.Enum))
	}
	if a.Pattern != "" {
		fmt.Fprintf(&b, ", Pattern: %q", a.Pattern)
	}
	if a.Min != nil {
		fmt.Fprintf(&b, ", Min: rtk.Ptr(%g)", *a.Min)
	}
	if a.Max != nil {
		fmt.Fprintf(&b, ", Max: rtk.Ptr(%g)", *a.Max)
	}
	if a.MinLength != nil {
		fmt.Fprintf(&b, ", MinLength: rtk.Ptr(%d)", *a.MinLength)
	}
	if a.MaxLength != nil {
		fmt.Fprintf(&b, ", MaxLength: rtk.Ptr(%d)", *a.MaxLength)
	}
	if a.MinItems != nil {
		fmt.Fprintf(&b, ", MinItems: rtk.Ptr(%d)", *a.MinItems)
	}
	if a.MaxItems != nil {
		fmt.Fprintf(&b, ", MaxItems: rtk.Ptr(%d)", *a.MaxItems)
	}
	if a.Nullable {
		b.WriteString(", Nullable: true")
	}
	if a.EnvKey != "" {
		fmt.Fprintf(&b, ", EnvKey: %q", a.EnvKey)
	}
	if a.ConfigKey != "" {
		fmt.Fprintf(&b, ", ConfigKey: %q", a.ConfigKey)
	}
	b.WriteString("},\n")
	return b.String()
}

// renderStdinSpecLiteral renders a *rtk.StdinSpec literal. Returns "nil"
// when in is nil.
func renderStdinSpecLiteral(in *rtk.StdinSpec) string {
	if in == nil {
		return "nil"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "&rtk.StdinSpec{Format: %q", in.Format)
	if len(in.Fields) > 0 {
		b.WriteString(", Fields: []rtk.StdinField{")
		for i, f := range in.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "{Name: %q, Type: %q", f.Name, f.Type)
			if f.Required {
				b.WriteString(", Required: true")
			}
			b.WriteString("}")
		}
		b.WriteString("}")
	}
	b.WriteString("}")
	return b.String()
}

// renderFlagSpecLiteralIndent is the template-facing wrapper around
// [renderFlagSpecLiteral] — it takes the FlagSpec by value (the form
// `range` over a slice produces) and uses the supplied indent string.
// The trailing comma+newline is trimmed so templates can format their
// own context.
func renderFlagSpecLiteralIndent(f rtk.FlagSpec, indent string) string {
	return strings.TrimSuffix(renderFlagSpecLiteral(&f, indent), ",\n")
}

// renderArgSpecLiteralIndent is the template-facing analog of
// [renderFlagSpecLiteralIndent] for arguments.
func renderArgSpecLiteralIndent(a rtk.ArgumentSpec, indent string) string {
	return strings.TrimSuffix(renderArgSpecLiteral(&a, indent), ",\n")
}
