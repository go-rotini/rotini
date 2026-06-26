package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini"
)

// ─────────────────────────────────────────────────────────────────────────────
// The `rotini generate` workflow.
// ─────────────────────────────────────────────────────────────────────────────.

// GenerateFn is the signature of [Processor.Generate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double.
type GenerateFn = func(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error

// Generate is a convenience over [Processor.Generate]: it builds a Processor for
// version and runs the generate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (init and tests).
func Generate(specPath, confPath string, watch bool, version string, onGenerate func(result string, err error)) error {
	return NewProcessor(version).Generate(specPath, confPath, watch, onGenerate)
}

// generate runs the generator phase over a loaded session (call load — and, through
// the pass, validate — first: invalid input must never reach codegen). It applies the
// built-in conf defaults, then emits the cmd and cmdgen packages plus the enabled doc
// features.
func (s *session) generate() error {
	applyConfDefaults(s.conf.conf, s.spec.spec.Command.Name)
	return generateAll(s.spec.spec, s.conf.conf, s.spec.path, s.version)
}

// ─────────────────────────────────────────────────────────────────────────────
// Code generation — the cli/cligen program (framework, rollup, stubs, literals).
// ─────────────────────────────────────────────────────────────────────────────.

// Generated programs reference the rotini runtime package under this name in
// rendered literals (the Definition, BindMeta, …); the templates hardcode the
// matching import.
const rotiniPkgName = "rotini"

// fieldDef is one generated struct field: a Go identifier, its type, and its
// `rotini` struct-tag content — a flag/argument logical name (empty for the
// per-command fields of an <Cmd>Inputs struct, which the binder maps by position).
type fieldDef struct {
	Field   string
	GoType  string
	Tag     string
	Import  string // Go import path backing GoType ("" for builtins); aliased form "alias path"
	Recon   string // recon struct-tag body for env/config fields (key + default/required/secret); "" otherwise
	EnvVar  string // explicit environment variable name for an env field (schema.variable); "" = snake-upper default
	EnvNest string // "<BASE>,<sep>" for a nested env input (schema.nesting): the var-family prefix and separator
	CfgFile string // a config input's pinned source file (schema.file): the value is read from that configuration_files entry ONLY
	Comment string // trailing line-comment on the generated field ("" for none) — e.g. the TextUnmarshaler contract nudge on explicitly-imported argv types
	// Constraint is the space-separated validation struct-tags for an env/config field
	// (e.g. `min:"1" max:"65535" pattern:"^x$"`), which the binder enforces over the
	// reconciled value; "" when the input declares no numeric/string/array constraints.
	Constraint string
}

// genCommand is the fully resolved description of one command node (root or
// sub-command) that the renderers consume.
type genCommand struct {
	prefix      string // PascalCase type prefix, e.g. "RotiniGenerate"
	handler     string // unexported handler struct name, e.g. "rotiniGenerateHandlers"
	filename    string // handler stub file name, e.g. "rotini_generate.go"
	flags       []fieldDef
	args        []fieldDef
	env         []fieldDef // <Prefix>Env fields (pure environment inputs)
	config      []fieldDef // <Prefix>Config fields (pure config-file inputs)
	stdinType   string     // Stdin field type, e.g. "*RotiniGenerateStdin"; "" when no stdin
	stdinFormat string     // stdin decode format, e.g. "yaml"; "" when no stdin
	inputs      []fieldDef // InputsFields for this command's <Prefix>Inputs

	// Inline-command passthrough (W9 / D-W9.7): the command's structure + inputs are
	// generated locally (this is still an own command), but its handler delegates to a
	// package instead of a generated stub. When passthrough is set the rollup emits
	// `return delegateAlias.delegateMethod()` and no stub file is seeded.
	passthrough    bool
	delegateAlias  string
	delegateMethod string
}

// layout holds the resolved package locations and import paths for a single
// generation pass. The cmd (handler) package and the cmdgen (framework) package
// may be the same package — even the same file — or two distinct packages:
//
//   - distinct packages (split): the rollup in the cmd package imports the
//     cmdgen package and refers to it qualified (cmdgen.ProgramHandlers);
//   - same package, distinct files: framework and rollup are two files in one
//     package, with unqualified references;
//   - same package and file (combined): framework and rollup are merged into a
//     single file, with unqualified references.
//
// The entrypoint package is optional: when the conf declares one, generate
// writes the binary's main.go there (create-once, never overwritten).
type layout struct {
	frameworkDir     string // absolute output dir for the framework (cmdgen) file
	frameworkPkgName string // cmdgen package name, e.g. "mycli" or "cmdgen"
	frameworkFile    string // framework file name, e.g. "zz_rotini.gen.go"
	frameworkImport  string // cmdgen import path; "" when cmd and cmdgen share a package
	frameworkQual    string // qualifier for the rollup's framework refs, e.g. "cmdgen."; "" when same package

	handlerDir     string // absolute output dir for stubs + rollup (cmd package)
	handlerPkgName string // cmd package name, e.g. "mycli"
	handlerImport  string // cmd package import path (the entrypoint's Program import)
	rollupFile     string // rollup file name, e.g. "zz_rotini.gen.go"

	entrypointDir  string // absolute output dir for the entrypoint main.go; "" when no entrypoint declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint declared

	combined bool // same package AND same file → framework+rollup merged into one file
}

// generateAll runs a single generation pass: it resolves the spec (expanding
// any composed $ref children), writes the framework file, creates missing
// handler stubs (an empty stub per command — the end-user wires them), writes
// the entrypoint main.go when the conf declares one, (re)writes the handler
// rollup, and prunes orphaned stubs. specPath is needed to resolve $ref paths
// relative to the spec.
func generateAll(spec *Spec, conf *Conf, specPath, version string) error {
	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}
	// Package arm (D-W9.4): refuse to generate code against a cross-major rotini library.
	if err := checkPackageVersion(moduleRoot, version); err != nil {
		return err
	}
	lay := resolveLayout(conf, moduleRoot, moduleName)
	gp, err := resolveTree(spec, specPath, moduleRoot, moduleName, version)
	if err != nil {
		return err
	}

	// Write rotini's embedded JSON Schemas to the project (opt-in via generate.schemas)
	// before codegen, so the editor `$schema=` references resolve even on a pass that
	// later fails. These files are never pruned.
	if err := writeSchemas(conf, moduleRoot); err != nil {
		return err
	}

	// For each enabled doc feature (help/man), the cligen file gains
	// embedded "<Prefix>" vars + a resolver, and each command's page is (re)written
	// under that feature's dir — rendered from the command's doc-fields, or written
	// verbatim when the command sets the feature's spec string. The conf's feature
	// dir is module-relative; the absolute dir is where files are written/pruned and
	// the cligen-package-relative path is what //go:embed references.
	feats := enabledFeatures(conf)
	frameworks := make([]templateFeature, 0, len(feats))
	outputs := make([]featureOutput, 0, len(feats))
	for _, f := range feats {
		var nodes []helpNode
		if f.desc.perShell {
			nodes = completionNodes() // completion: per shell, not per command
		} else {
			nodes = flattenFeature(gp, f.desc)
		}
		absEmbedDir := filepath.Join(moduleRoot, filepath.FromSlash(f.cfg.EmbedDir))
		absTemplateDir := filepath.Join(moduleRoot, filepath.FromSlash(f.cfg.TemplateDir))

		// The //go:embed path only matters in embed mode, and ONLY then must the
		// embed_dir resolve under the cmdgen package (embed can't reach outside
		// it). An inline feature writes no embedded file, so embed_dir is
		// unconstrained; template_dir is never embedded, so it always is.
		embedRel := ""
		if f.cfg.Embed {
			rel, err := filepath.Rel(lay.frameworkDir, absEmbedDir)
			if err != nil {
				return fmt.Errorf("feature %s embed_dir %q is not under the cmdgen package: %w", f.desc.name, f.cfg.EmbedDir, err)
			}
			if strings.HasPrefix(rel, "..") {
				return fmt.Errorf("generate.features.%s.embed_dir %q must resolve under the cmdgen package %q so //go:embed can reach it", f.desc.name, f.cfg.EmbedDir, filepath.ToSlash(conf.Generate.Packages.Cmdgen.Package))
			}
			embedRel = filepath.ToSlash(rel)
		}

		var contents []string
		var err error
		if f.desc.perShell {
			contents, err = completionContents(gp.rootName, nodes)
		} else {
			contents, err = docFeatureContents(absTemplateDir, nodes, f.desc, f.cfg.Template)
		}
		if err != nil {
			return err
		}

		frameworks = append(frameworks, buildFeatureFramework(nodes, embedRel, f.desc, f.cfg.Embed, contents))
		outputs = append(outputs, featureOutput{desc: f.desc, absEmbedDir: absEmbedDir, nodes: nodes, contents: contents, embed: f.cfg.Embed})
	}

	// Render the framework (cmdgen) and the handler rollup (cmd). When cmd and
	// cmdgen resolve to the same package AND file, the two are merged into a single
	// file (rollup body first, then framework body); otherwise they are written to
	// their own files (two files in one package, or two packages).
	fwContent, err := renderFrameworkFile(gp, lay, frameworks)
	if err != nil {
		return err
	}
	rollupContent, err := renderHandlerRollup(gp, lay)
	if err != nil {
		return err
	}
	if lay.combined {
		merged, err := mergeGenFile(lay.handlerPkgName, rollupContent, fwContent)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), merged); err != nil {
			return err
		}
	} else {
		if err := writeGeneratedFile(filepath.Join(lay.frameworkDir, lay.frameworkFile), fwContent); err != nil {
			return err
		}
		if err := writeGeneratedFile(filepath.Join(lay.handlerDir, lay.rollupFile), rollupContent); err != nil {
			return err
		}
	}

	for _, o := range outputs {
		// Inline features write no output files — their content is in the .go.
		// (pruneCligen removes any stale files left from a previous embed:true.)
		if !o.embed {
			continue
		}
		if err := writeFeatureOutputs(o.absEmbedDir, o.nodes, o.contents, o.desc); err != nil {
			return err
		}
	}
	if err := writeHandlerStubs(gp, lay); err != nil {
		return err
	}
	if err := writeEntrypoint(lay, string(detectFileFormat(specPath))); err != nil {
		return err
	}
	// Pruning is implicit (always-on): drop orphaned cmd stubs and orphaned cmdgen
	// feature outputs, sparing only the per-package `keep` paths (and test files
	// and the editable feature templates).
	if err := pruneStubs(gp, lay, conf.Generate.Packages.Cmd.Keep); err != nil {
		return err
	}
	if err := pruneCligen(lay, conf.Generate.Packages.Cmdgen.Keep, outputs); err != nil {
		return err
	}
	return nil
}

// writeSchemas writes rotini's embedded JSON Schemas to the project paths declared
// under generate.schemas (opt-in). Each path is module-root-relative; the embedded bytes
// are written verbatim — unchanged on a no-op pass (stable mtime), overwritten otherwise —
// so an editor `# yaml-language-server: $schema=<path>` reference can resolve the schema
// locally instead of fetching a remote URL. These files are codegen output but are NOT
// pruned (they are not command-derived; the .json suffix never matches a stub/feature
// prune set either). An absent block, or an absent conf/spec entry, writes nothing.
func writeSchemas(conf *Conf, moduleRoot string) error {
	if conf.Generate == nil || conf.Generate.Schemas == nil {
		return nil
	}
	write := func(label string, sc *SchemaConfig, content []byte) error {
		if sc == nil || sc.Path == "" {
			return nil
		}
		rel := filepath.FromSlash(sc.Path)
		if filepath.IsAbs(rel) {
			return fmt.Errorf("generate.schemas.%s.path %q must be module-root-relative, not absolute", label, sc.Path)
		}
		abs := filepath.Join(moduleRoot, rel)
		if r, err := filepath.Rel(moduleRoot, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generate.schemas.%s.path %q must resolve under the module root", label, sc.Path)
		}
		return writeGeneratedFile(abs, content)
	}
	s := conf.Generate.Schemas
	if err := write("conf", s.Conf, schemaConfFileBytes); err != nil {
		return err
	}
	return write("spec", s.Spec, schemaSpecFileBytes)
}

// confFeature pairs a doc-feature descriptor with its conf entry.
type confFeature struct {
	desc docFeature
	cfg  *Feature
}

// featureOutput is one enabled feature's resolved absolute output dir +
// per-command nodes, used for writing and pruning its output dir.
type featureOutput struct {
	desc        docFeature
	absEmbedDir string // where rendered output files are written (embed mode)
	nodes       []helpNode
	contents    []string // final per-node content (parallel to nodes), rendered/verbatim/stripped
	embed       bool     // true: write contents to files (//go:embed); false: inline in the .go, write no output files
}

// featureConfigs pairs every doc feature with its conf entry (nil when unset).
// Requires conf.Generate to be non-nil (guaranteed after applyConfDefaults).
func featureConfigs(conf *Conf) []confFeature {
	feats := conf.Generate.Features
	if feats == nil {
		return nil
	}
	return []confFeature{
		{helpFeatureDesc, feats.Help},
		{manFeatureDesc, feats.Man},
		{markdownFeatureDesc, feats.Markdown},
		{completionFeatureDesc, feats.Completion},
	}
}

// enabledFeatures returns the doc features toggled on, in help→man order.
func enabledFeatures(conf *Conf) []confFeature {
	var out []confFeature
	for _, f := range featureConfigs(conf) {
		if f.cfg != nil && f.cfg.Enabled {
			out = append(out, f)
		}
	}
	return out
}

// inputsFields returns the fields of a command's <Prefix>Inputs struct: one per
// ancestor command (root first, then each intermediate) plus the command itself,
// in root→leaf order. Each field is named after the command's PascalCase prefix
// and typed as that prefix's CommandInputs. There is deliberately no struct tag:
// the binder maps fields to resolved-chain frames by position (aligned at the
// leaf), so command names can never collide along a path.
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
// a flag with an env/config *fallback* still lives here and is reconciled by the binder.
func flagFields(in *Inputs) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Flags))
	for _, f := range in.Flags {
		fields = append(fields, fieldDef{
			Field: toPascalCase(f.Name), GoType: goFieldType(f.Schema), Tag: f.Name,
			Import: fieldImport(f.Schema), Recon: flagReconKey(f.Schema),
			Comment: contractComment(f.Schema),
		})
	}
	return fields
}

// flagReconKey is a flag's reconciliation key — its config key (schema.key) — when
// the flag declares a config fallback, else "" (an argv-only flag, no recon tag).
// The binder reconciles such a flag argv > env (SNAKE_UPPER of the key) > config > default.
func flagReconKey(schema *InputSchema) string {
	if schema != nil && schema.Key != "" {
		return schema.Key
	}
	return ""
}

// envFields returns the <Prefix>Env struct fields: one per pure environment input.
// The recon key is the input name (recon's env source maps it to SNAKE_UPPER).
func envFields(in *Inputs, envPrefix string) []fieldDef {
	if in == nil {
		return nil
	}
	fields := make([]fieldDef, 0, len(in.Env))
	for _, e := range in.Env {
		fd := fieldDef{
			Field: toPascalCase(e.Name), GoType: goFieldType(e.Schema), Tag: e.Name,
			Import: fieldImport(e.Schema), Recon: reconTag(e.Name, e.Schema), EnvVar: envVarOf(e.Schema),
			Constraint: constraintTags(e.Schema),
		}
		// A nested input's variable is a family PREFIX, not the value's own env
		// var — it rides in the envnest tag instead of env:, and rotini (not
		// recon) enforces required, since recon resolves leaf keys only.
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

// constraintTags renders an input's numeric/string/array constraints as space-separated
// validation struct-tags (e.g. `min:"1" max:"65535" pattern:"^x$"`) for the binder to
// enforce, or "" when none are set. Mirrors the FlagDef/ArgDef constraints A1 enforces
// for argv, but carried on the env/config field itself since channels have no Definition.
func constraintTags(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	var parts []string
	if schema.Minimum != nil {
		parts = append(parts, `min:"`+strconv.FormatFloat(*schema.Minimum, 'g', -1, 64)+`"`)
	}
	if schema.Maximum != nil {
		parts = append(parts, `max:"`+strconv.FormatFloat(*schema.Maximum, 'g', -1, 64)+`"`)
	}
	if schema.ExclusiveMinimum != nil {
		parts = append(parts, `xmin:"`+strconv.FormatFloat(*schema.ExclusiveMinimum, 'g', -1, 64)+`"`)
	}
	if schema.ExclusiveMaximum != nil {
		parts = append(parts, `xmax:"`+strconv.FormatFloat(*schema.ExclusiveMaximum, 'g', -1, 64)+`"`)
	}
	if schema.MultipleOf != nil {
		parts = append(parts, `multipleof:"`+strconv.FormatFloat(*schema.MultipleOf, 'g', -1, 64)+`"`)
	}
	if schema.MinLength != 0 {
		parts = append(parts, `minlen:"`+strconv.Itoa(schema.MinLength)+`"`)
	}
	if schema.MaxLength != 0 {
		parts = append(parts, `maxlen:"`+strconv.Itoa(schema.MaxLength)+`"`)
	}
	if schema.MinItems != 0 {
		parts = append(parts, `minitems:"`+strconv.Itoa(schema.MinItems)+`"`)
	}
	if schema.MaxItems != 0 {
		parts = append(parts, `maxitems:"`+strconv.Itoa(schema.MaxItems)+`"`)
	}
	if schema.Pattern != "" {
		parts = append(parts, `pattern:"`+schema.Pattern+`"`)
	}
	return strings.Join(parts, " ")
}

// envVarOf returns an env input's explicit environment variable (schema.variable),
// or "" to let the binder use recon's snake-upper default for the key.
func envVarOf(schema *InputSchema) string {
	if schema != nil {
		return schema.Variable
	}
	return ""
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

// stdinTypeExpr returns the Go type for a command's Stdin field — "*<Prefix>Stdin"
// when the command declares a typed stdin payload, else "" (no Stdin field).
func stdinTypeExpr(prefix string, in *Inputs) string {
	if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
		return ""
	}
	return "*" + prefix + "Stdin"
}

// stdinFormatExpr returns the value of a command's `stdin:"<format>[,required]"`
// struct tag: the decode format (defaulting to json), with ",required" appended
// when the spec marks the payload required — the binder then rejects an empty
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
	case "duration":
		return "time.Duration"
	case "time", "datetime", "date":
		return "time.Time"
	default:
		return t
	}
}

// writeInputDefsLiteral appends the Flags/Arguments/FlagGroups/FlagDependencies
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
func renderDefinition(gp *genProgram) string {
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
func renderBindMeta(gp *genProgram) string {
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
	// Only the version handshake applies to open-ended discovery (lintRemoteDiscoveryVerify
	// rejects sha256/signature here), so emit just that rung.
	if v := d.Verify; v != nil && v.Version {
		b.WriteString(", Verify: &" + rotiniPkgName + ".RemoteVerify{Version: true}")
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
		if v := rc.Verify; v != nil && (v.Version || v.Sha256 != "" || v.Signature != nil) {
			b.WriteString(", Verify: &rotini.RemoteVerify{")
			sep := ""
			if v.Version {
				b.WriteString("Version: true")
				sep = ", "
			}
			if v.Sha256 != "" {
				b.WriteString(sep + "SHA256: " + strconv.Quote(v.Sha256))
				sep = ", "
			}
			if s := v.Signature; s != nil {
				b.WriteString(sep + "Signature: &rotini.RemoteSignatureVerify{Issuer: " + strconv.Quote(s.Issuer) + ", Subject: " + strconv.Quote(s.Subject) + "}")
			}
			b.WriteString("}")
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
	// The explicit type parameter matters: Ptr(1) would infer *int and the
	// generated literal would not compile against the *float64 field.
	ptr := func(f float64) string {
		return rotiniPkgName + ".Ptr[float64](" + strconv.FormatFloat(f, 'g', -1, 64) + ")"
	}
	var parts []string
	if schema.Minimum != nil {
		parts = append(parts, "Minimum: "+ptr(*schema.Minimum))
	}
	if schema.Maximum != nil {
		parts = append(parts, "Maximum: "+ptr(*schema.Maximum))
	}
	if schema.ExclusiveMinimum != nil {
		parts = append(parts, "ExclusiveMinimum: "+ptr(*schema.ExclusiveMinimum))
	}
	if schema.ExclusiveMaximum != nil {
		parts = append(parts, "ExclusiveMaximum: "+ptr(*schema.ExclusiveMaximum))
	}
	if schema.MultipleOf != nil {
		parts = append(parts, "MultipleOf: "+ptr(*schema.MultipleOf))
	}
	if schema.MinLength != 0 {
		parts = append(parts, "MinLength: "+strconv.Itoa(schema.MinLength))
	}
	if schema.MaxLength != 0 {
		parts = append(parts, "MaxLength: "+strconv.Itoa(schema.MaxLength))
	}
	if schema.MinItems != 0 {
		parts = append(parts, "MinItems: "+strconv.Itoa(schema.MinItems))
	}
	if schema.MaxItems != 0 {
		parts = append(parts, "MaxItems: "+strconv.Itoa(schema.MaxItems))
	}
	if schema.Pattern != "" {
		parts = append(parts, "Pattern: "+strconv.Quote(schema.Pattern))
	}
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

// toTemplateFields converts resolved fieldDefs to renderer input fields,
// assembling each field's complete struct-tag literal from its parts. A fieldDef
// with no rotini tag (the <Prefix>Inputs fields, which the binder maps by
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

// renderFrameworkFile renders the framework file: the ProgramHandlers aggregate
// interface plus the typed input structs for every command. The caller writes it
// (to its own file, or merged with the rollup when cli and cligen are combined).
// It is fully generated and carries a DO NOT EDIT banner.
func renderFrameworkFile(gp *genProgram, lay layout, features []templateFeature) ([]byte, error) {
	own := gp.ownCommands()

	blocks := make([]templateInputBlock, 0, len(own))
	imports := map[string]bool{}
	noteImport := func(imp string) {
		if imp != "" {
			imports[imp] = true
		}
	}
	for _, c := range own {
		blocks = append(blocks, templateInputBlock{
			Prefix:       c.prefix,
			Flags:        toTemplateFields(c.flags),
			Arguments:    toTemplateFields(c.args),
			Env:          toTemplateFields(c.env),
			Config:       toTemplateFields(c.config),
			StdinType:    c.stdinType,
			StdinFormat:  c.stdinFormat,
			InputsFields: toTemplateFields(c.inputs),
		})
		for _, fs := range [][]fieldDef{c.flags, c.args, c.env, c.config} {
			for _, f := range fs {
				noteImport(f.Import)
			}
		}
	}

	outputTypes, err := buildOutputTypes(gp, lay.frameworkPkgName)
	if err != nil {
		return nil, err
	}

	return renderRotiniFile(templateRotiniData{
		Package:     lay.frameworkPkgName,
		Imports:     renderImports(imports),
		Methods:     gp.methods(),
		Definition:  renderDefinition(gp),
		Blocks:      blocks,
		OutputTypes: outputTypes,
		BindMeta:    renderBindMeta(gp),
		Features:    features,
		EmbedImport: anyEmbed(features),
	})
}

// anyEmbed reports whether any feature emits a //go:embed-backed var (so the
// generated file must import the embed package). Inline-only features need no
// embed import.
func anyEmbed(features []templateFeature) bool {
	for _, f := range features {
		for _, v := range f.Vars {
			if v.Embed != "" {
				return true
			}
		}
	}
	return false
}

// writeHandlerStubs creates a per-command handler stub for the root command and
// every OWN sub-command, but only when the file does not already exist — stubs
// are user-editable, so an existing stub is never overwritten. Composed
// commands have no stub here; their handlers live in the child's package.
//
// initStyle (only `rotini initialize` sets it) seeds the root command and the
// top-level help/version commands from the wired init templates instead of the
// empty stub, so a fresh CLI ships with working -h/--help, -v/--version, and
// help/version commands. To opt out, delete those handler files and run a
// normal `rotini generate` — the empty stubs are seeded in their place.
func writeHandlerStubs(gp *genProgram, lay layout) error {
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			continue // inline-passthrough: the package owns the handler, no stub seeded
		}
		path := filepath.Join(lay.handlerDir, c.filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		content, err := renderHandlerStubFile(lay.handlerPkgName, c.handler)
		if err != nil {
			return err
		}
		if err := writeGeneratedFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

// writeEntrypoint writes the binary's main.go to the conf-declared entrypoint
// package — create-once: the file binds user-owned build metadata (version/
// commit/date), so an existing main.go is never overwritten. A conf without an
// entrypoint writes nothing. extension is the spec/conf file extension (e.g.
// "yaml") baked into the //go:generate directive so it points at the seeded files.
func writeEntrypoint(lay layout, extension string) error {
	if lay.entrypointDir == "" {
		return nil
	}
	path := filepath.Join(lay.entrypointDir, lay.entrypointFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	// The generated package is imported aliased as "cmd" so the reference never
	// collides with the rotini runtime package (also named "rotini").
	content, err := renderMainFile(lay.handlerImport, "cmd", extension)
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}

// renderHandlerRollup renders the handler rollup: the unexported handlers
// struct, the ProgramHandlers assertion, the Program var, the Handlers accessor,
// and one method per command — own commands return a local stub, composed
// commands delegate to the child's cli package. References to the framework
// (ProgramHandlers, NewProgram) are unqualified when cli and cligen share a
// package, else qualified with the cligen package name. The caller writes it (to
// its own file, or merged with the framework when combined).
func renderHandlerRollup(gp *genProgram, lay layout) ([]byte, error) {
	methods := make([]templateHandlersMethod, 0, 1+len(gp.own)+len(gp.composed))
	for _, c := range gp.ownCommands() {
		if c.passthrough {
			// Inline-command passthrough (D-W9.7): own command (types generated
			// locally) whose handler delegates to a package instead of a stub.
			methods = append(methods, templateHandlersMethod{
				Method:         c.prefix,
				Passthrough:    true,
				DelegateAlias:  c.delegateAlias,
				DelegateMethod: c.delegateMethod,
			})
			continue
		}
		methods = append(methods, templateHandlersMethod{Method: c.prefix, HandlerType: c.handler})
	}
	for _, c := range gp.composed {
		methods = append(methods, templateHandlersMethod{
			Method:         c.prefix,
			Composed:       true,
			Passthrough:    c.passthrough,
			DelegateAlias:  c.delegateAlias,
			DelegateMethod: c.delegateMethod,
		})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Method < methods[j].Method })

	return renderHandlersFile(templateHandlersData{
		Package:         lay.handlerPkgName,
		FrameworkImport: lay.frameworkImport, // "" when cmd and cmdgen share a package
		FrameworkQual:   lay.frameworkQual,   // e.g. "cmdgen."; "" when same package
		ChildImports:    gp.childImports,
		Methods:         methods,
	})
}

// pruneStubs removes handler .go files that no longer correspond to an own
// command, preserving the rollup file, the keep list, and any test files. The
// cmd package is flat, so keepList entries (package-relative) are just file names
// for its top-level stubs. When cmd and cmdgen share a package (two-files-one-
// package layout), the framework file also lives here, so it is protected too —
// otherwise it would be pruned as an orphan (and likewise the entrypoint main.go
// when the entrypoint shares the cmd package).
func pruneStubs(gp *genProgram, lay layout, keepList []string) error {
	protected := map[string]bool{
		gp.root.filename:  true,
		lay.rollupFile:    true,
		lay.frameworkFile: true,
	}
	if lay.entrypointDir == lay.handlerDir && lay.entrypointFile != "" {
		protected[lay.entrypointFile] = true
	}
	for _, c := range gp.own {
		protected[c.filename] = true
	}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}

	entries, err := os.ReadDir(lay.handlerDir)
	if err != nil {
		return fmt.Errorf("read handler dir %s: %w", lay.handlerDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if protected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(lay.handlerDir, name)); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
	}
	return nil
}

// pruneCligen removes orphaned rotini-managed outputs in each enabled feature's
// dir (under the cmdgen package) — the per-command pages for commands no longer
// in the spec. Only files matching the feature's unique suffix AND prefix are
// candidates, so features sharing one embed dir never prune each other's files.
// The editable per-feature template, test files, and any keep-listed
// (package-relative) path are preserved. Top-level cmdgen files (the gen file)
// are never auto-removed. keepList entries are package-relative to the cmdgen
// package.
func pruneCligen(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// The editable template is always protected (it is the author's content,
		// never pruned even when template:false leaves it inert). The current
		// command set's output pages are protected only in embed mode — an inline
		// feature writes none, so any on-disk pages are stale and get pruned.
		// Pruning scans the embed_dir for stale OUTPUT files. The editable
		// template lives in template_dir (a different tree) and is rotini's only
		// managed file there, so it is never a prune candidate. The current
		// command set's output files are protected only in embed mode — inline
		// features write none, so any on-disk pages are stale and get pruned.
		protected := map[string]bool{}
		if o.embed {
			for _, n := range o.nodes {
				protected[n.file] = true
			}
		}

		entries, err := os.ReadDir(o.absEmbedDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s embed_dir %s: %w", o.desc.name, o.absEmbedDir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, o.desc.ext) || strings.HasSuffix(name, "_test"+o.desc.ext) {
				continue
			}
			if o.desc.filePrefix != "" && !strings.HasPrefix(name, o.desc.filePrefix) {
				continue
			}
			if protected[name] {
				continue
			}
			// keep entries are package-relative (to the cmdgen package).
			rel := name
			if r, err := filepath.Rel(lay.frameworkDir, filepath.Join(o.absEmbedDir, name)); err == nil {
				rel = filepath.ToSlash(r)
			}
			if keep[rel] {
				continue
			}
			if err := os.Remove(filepath.Join(o.absEmbedDir, name)); err != nil {
				return fmt.Errorf("prune %s: %w", rel, err)
			}
		}
	}
	return nil
}

// resolveLayout turns the (defaulted) conf package settings into absolute
// output directories, package names, the framework import path, and the
// same-package/combined flags. cmd and cmdgen may name the same package (refs
// become unqualified) and even the same file (framework + rollup are merged).
// The entrypoint is optional — its layout fields are "" when undeclared.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) layout {
	cmd := conf.Generate.Packages.Cmd
	cmdgen := conf.Generate.Packages.Cmdgen

	cmdPkgDir := filepath.ToSlash(cmd.Package)
	cmdgenPkgDir := filepath.ToSlash(cmdgen.Package)
	samePackage := cmdPkgDir == cmdgenPkgDir
	combined := samePackage && cmd.File == cmdgen.File

	frameworkImport := ""
	frameworkQual := ""
	if !samePackage {
		frameworkImport = moduleName + "/" + cmdgenPkgDir
		frameworkQual = goPkgName(cmdgenPkgDir) + "."
	}

	lay := layout{
		frameworkDir:     filepath.Join(moduleRoot, filepath.FromSlash(cmdgenPkgDir)),
		frameworkPkgName: goPkgName(cmdgenPkgDir),
		frameworkFile:    cmdgen.File,
		frameworkImport:  frameworkImport,
		frameworkQual:    frameworkQual,

		handlerDir:     filepath.Join(moduleRoot, filepath.FromSlash(cmdPkgDir)),
		handlerPkgName: goPkgName(cmdPkgDir),
		handlerImport:  moduleName + "/" + cmdPkgDir,
		rollupFile:     cmd.File,

		combined: combined,
	}

	if ep := conf.Generate.Packages.Main; ep != nil && ep.Package != "" {
		file := ep.File
		if file == "" {
			file = "main.go"
		}
		lay.entrypointDir = filepath.Join(moduleRoot, filepath.FromSlash(ep.Package))
		lay.entrypointFile = file
	}

	return lay
}

// goPkgName derives the Go package NAME from a package directory path: its last
// segment with every character that is not a valid Go identifier rune dropped.
// A CLI name (and so its scaffold directory) may legitimately contain hyphens —
// "agentic-cooking" — but a Go package name cannot, so the package is named
// "agenticcooking". The import PATH keeps the original segment (import paths may
// contain hyphens); only the package name (and its qualifier) is sanitized.
func goPkgName(dir string) string {
	var b strings.Builder
	for _, r := range filepath.Base(dir) {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyConfDefaults fills in the sane rotini conf defaults for any unset
// generation settings, so a missing or partial conf still generates. The
// default is one self-contained module-internal package: both cmd and cmdgen
// point at "internal/cmd/<root>" / "zz_rotini.gen.go" (so framework + rollup
// merge into a single file). The entrypoint gets no default — main.go is only
// written when the conf declares one. rootName is the spec's root command name
// (e.g. "rotini"), used to build the default package path.
func applyConfDefaults(conf *Conf, rootName string) {
	if conf.Generate == nil {
		conf.Generate = &GenerateConfig{}
	}
	if conf.Generate.Packages == nil {
		conf.Generate.Packages = &PackagesConfig{}
	}
	pkgs := conf.Generate.Packages
	if pkgs.Cmd == nil {
		pkgs.Cmd = &PackageConfig{}
	}
	if pkgs.Cmdgen == nil {
		pkgs.Cmdgen = &PackageConfig{}
	}
	defaultPkg := "internal/cmd/" + rootName
	const defaultFile = "zz_rotini.gen.go"
	for _, p := range []*PackageConfig{pkgs.Cmd, pkgs.Cmdgen} {
		if p.Package == "" {
			p.Package = defaultPkg
		}
		if p.File == "" {
			p.File = defaultFile
		}
	}
	// Each present feature defaults its two dirs from the cmdgen package
	// (module-relative): rendered OUTPUT files to "<cmdgen-package>/renders"
	// (always under cmdgen so //go:embed can reach them in embed mode), and the
	// editable TEMPLATE to "<cmdgen-package>/templates". Co-located features
	// cannot collide: each carries a feature-unique suffix/prefix (see
	// docFeature) and pruning is scoped to them.
	cmdgenDir := filepath.ToSlash(pkgs.Cmdgen.Package)
	for _, f := range featureConfigs(conf) {
		if f.cfg == nil {
			continue
		}
		if f.cfg.EmbedDir == "" {
			f.cfg.EmbedDir = cmdgenDir + "/renders"
		}
		if f.cfg.TemplateDir == "" {
			f.cfg.TemplateDir = cmdgenDir + "/templates"
		}
	}
}

// fieldImport returns the Go import path backing a field's schema: the explicit
// spec `import:` when set, otherwise the import rotini knows is needed for its own
// built-in type aliases (duration/time/datetime/date → "time"). "" means no import.
func fieldImport(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if imp := strings.TrimSpace(schema.Import); imp != "" {
		return imp
	}
	// An array's element type carries the import: explicit items.import first,
	// then the built-in vocabulary (items duration → "time").
	if schema.Items != nil && jsonSchemaTypeToGo(schema.Type) == "[]string" {
		if imp := strings.TrimSpace(schema.Items.Import); imp != "" {
			return imp
		}
		return builtinImport(schema.Items.Type)
	}
	return builtinImport(schema.Type)
}

// builtinImport returns the import path rotini's own type vocabulary requires, or
// "" when the type needs none. Only the time-family aliases (which jsonSchemaTypeToGo
// maps to time.Time/time.Duration) carry an implicit import.
func builtinImport(rotiniType string) string {
	switch rotiniType {
	case "duration", "time", "datetime", "date":
		return "time"
	}
	return ""
}

// parseAliasPath splits the `alias path` external-Go-binding form (shared by an input
// type's `import:` and a command's `handler.import` — D-W9.5) into its alias and path;
// a bare path derives its alias from the last segment.
func parseAliasPath(imp string) (alias, importPath string) {
	imp = strings.TrimSpace(imp)
	if a, p, ok := strings.Cut(imp, " "); ok {
		return strings.TrimSpace(a), strings.TrimSpace(p)
	}
	return identAlias(filepath.Base(imp)), imp
}

// renderImports turns a set of spec `import:` values into sorted Go import specs:
// a plain path becomes "path"; the aliased form "alias path" becomes alias "path".
func renderImports(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for imp := range set {
		if alias, path, ok := strings.Cut(imp, " "); ok {
			out = append(out, alias+" "+strconv.Quote(strings.TrimSpace(path)))
		} else {
			out = append(out, strconv.Quote(imp))
		}
	}
	sort.Strings(out)
	return out
}

// toPascalCase converts a name to PascalCase, treating '-', '_' and ' ' as word
// boundaries (e.g. "foo_bar" -> "FooBar", "generate" -> "Generate").
func toPascalCase(s string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range s {
		if r == '-' || r == '_' || r == ' ' {
			capitalize = true
			continue
		}
		if capitalize {
			b.WriteRune(unicode.ToUpper(r))
			capitalize = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lowerFirst returns s with its first rune lower-cased.
func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// goReservedFilenames are the trailing "_"-separated tokens the go tool reads
// specially from a file's name alone: "test" (a "_test.go" test file, excluded from
// the normal build) and the GOOS/GOARCH names (an implicit build constraint, e.g.
// "app_windows.go" builds only on Windows). Kept as one set since stubFilename only
// needs membership, not which rule matched.
var goReservedFilenames = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []string{
		"test",
		// GOOS
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
		"wasip1", "windows", "zos",
		// GOARCH
		"386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64",
		"mips", "mips64", "mips64le", "mips64p32", "mips64p32le", "mipsle", "ppc",
		"ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64",
		"wasm",
	} {
		m[s] = true
	}
	return m
}()

// reservedTrailingToken reports whether stem's trailing "_"-separated token is one the
// go tool reads specially from a file's name — "test" (a "_test.go" test file) or a
// GOOS/GOARCH (an implicit build constraint).
func reservedTrailingToken(stem string) bool {
	parts := strings.Split(stem, "_")
	return goReservedFilenames[parts[len(parts)-1]]
}

// stubFilename builds a handler-stub file name from base (a command's root name or
// "<root>_<path>"), escaping the names the go tool would read specially from the
// filename alone — a "_test.go" test file, or a "_<GOOS>.go"/"_<GOARCH>.go" build
// constraint — by appending a trailing underscore. That makes the trailing
// "_"-separated token empty, which matches none of those rules, so a command named
// "test"/"windows"/"wasm"/… still compiles into the ordinary build.
func stubFilename(base string) string {
	if reservedTrailingToken(base) {
		base += "_"
	}
	return base + ".go"
}

// commandStubFilename returns a command's handler-stub file name: its explicit
// `filename` override when set, else the derived "<root>[_<path>].go" (reserved-name
// escaped by stubFilename). path is the underscore-joined command path relative to the
// root, "" for the root command itself. The same derivation is shared by codegen (to
// name the stub) and lintHandlerFilenames (to validate uniqueness), so they agree.
func commandStubFilename(rootName, path, override string) string {
	if override != "" {
		return override
	}
	base := rootName
	if path != "" {
		base += "_" + path
	}
	return stubFilename(base)
}

// ─────────────────────────────────────────────────────────────────────────────
// The resolved command tree — spec (+ $ref composition) → genProgram.
// ─────────────────────────────────────────────────────────────────────────────.

// genProgram is a parent spec resolved for code generation: its own command
// tree (inline commands — emit types, stubs, and a rollup method that returns a
// local stub) plus any statically composed commands pulled in via `$ref` (emit
// a rollup method that delegates to the child's cli package; no types or stubs).
type genProgram struct {
	rootName        string
	rootPascal      string
	rootInputs      *Inputs
	rootRemotes     []RemoteCommandSpec // root-level remote/co-located sub-commands
	rootHelp        cmdHelp             // root command's flattened help fields
	rootOutput      *Schema             // root command's output type (nil when unset)
	rootDiscovery   *RemoteDiscovery    // root command's plugin discovery (nil = off)
	rootPassthrough bool                // root command's passthrough (raw positionals)
	schemas         map[string]Schema   // document-level named schemas (for output codegen)
	configFiles     []scopedConfigFile  // per-command config-file sources, tagged with their command path (for the binder's cascade)
	envPrefix       string              // document-level env_prefix for DERIVED env-var names
	version         string              // running rotini version, for the cross-tree version guard on composed specs ("" → skipped)

	root         genCommand               // the root command (own)
	own          []genCommand             // inline sub-commands, sorted by prefix
	composed     []composedCmd            // composed sub-commands, sorted by prefix
	tree         []rnode                  // full resolved tree (own + grafted), for the Definition
	childImports []templateHandlersImport // unique child cli imports for the rollup
}

// ownCommands returns the commands this program emits types and stubs for —
// the root, then every own (inline) sub-command.
func (gp *genProgram) ownCommands() []genCommand {
	return append([]genCommand{gp.root}, gp.own...)
}

// methods returns the ProgramHandlers method names: the root, then every own
// and composed sub-command, sorted.
func (gp *genProgram) methods() []string {
	out := make([]string, 0, 1+len(gp.own)+len(gp.composed))
	out = append(out, gp.root.prefix)
	for _, c := range gp.own {
		out = append(out, c.prefix)
	}
	for _, c := range gp.composed {
		out = append(out, c.prefix)
	}
	sort.Strings(out)
	return out
}

// rnode is one node of the resolved command tree used to render the Definition.
type rnode struct {
	name                  string
	prefix                string // ProgramHandlers method (the dispatch Handler), e.g. "MycliparentMyclichild1"
	aliases               []string
	inputs                *Inputs
	help                  cmdHelp             // flattened help fields; for a composed root, from the child spec
	output                *Schema             // command's output type (own commands only; nil for composed)
	discovery             *RemoteDiscovery    // command's plugin discovery (nil = off)
	hidden                bool                // omit from the parent's generated Commands list
	group                 string              // group label that buckets this command in the parent's Commands list
	deprecated            string              // deprecation note for the parent's Commands list (help annotation)
	deprecatedIdentifiers []string            // deprecated aliases of this command (runtime Deprecations)
	passthrough           bool                // every token after this command is a raw positional
	composed              bool                // grafted from a $ref'd child (its types live in the child's cligen)
	remotes               []RemoteCommandSpec // co-located remote sub-commands declared on this command
	children              []rnode
}

// composedCmd is a command supplied by a composed child: the parent's rollup
// method (prefix) delegates to delegateAlias.delegateMethod(). When passthrough is
// set (W9 handler-code composition), the call is alias.method() — the package exports
// the constructor directly; otherwise it is alias.Handlers().method() (a generated cli).
type composedCmd struct {
	prefix         string
	delegateAlias  string
	delegateMethod string
	passthrough    bool
}

// composeCtx threads composition state down a composed subtree.
type composeCtx struct {
	composed    bool
	rootPath    string // underscore path of the composed subtree's root in the parent
	childPascal string // root method name the subtree delegates under (child's name, or the handler convention)
	alias       string // import alias of the handler package (composed child cli, or a W9 passthrough package)
	passthrough bool   // delegate via alias.method() (W9 passthrough) instead of alias.Handlers().method()
}

// resolveTree resolves spec into a genProgram, loading any `$ref`'d child specs
// (relative to specPath) and grafting them as composed subtrees.
// scopedConfigFile is a config_files source paired with the command path it is
// declared on — the Scope the binder matches against the resolved chain to honor
// the cascade (D-W3.1). The path is name-joined ("root", "root/sub", …), the same
// form Binder.chainConfigFiles computes from the chain.
type scopedConfigFile struct {
	ConfigurationFile

	Scope string
}

// allScopedConfigFiles gathers every command's config_files across the tree,
// tagging each with its command path. Replaces the Phase-1 flat allConfigFiles
// for the descriptor: the binder needs the scope to chain-scope loading.
func allScopedConfigFiles(spec *Spec) []scopedConfigFile {
	var out []scopedConfigFile
	walkCommands(spec, func(c *Command, path string) {
		if c.inputs() == nil {
			return
		}
		for _, cf := range c.inputs().ConfigFiles {
			out = append(out, scopedConfigFile{ConfigurationFile: cf, Scope: path})
		}
	})
	return out
}

func resolveTree(spec *Spec, specPath, moduleRoot, moduleName, version string) (*genProgram, error) {
	root := spec.Command
	if root.Ref != "" || root.Name == "" {
		return nil, errors.New("root command must have a name (the top-level \"command\" cannot use $ref)")
	}
	gp := &genProgram{
		rootName:        root.Name,
		rootPascal:      toPascalCase(root.Name),
		rootInputs:      root.inputs(),
		rootRemotes:     root.RemoteCommands,
		rootHelp:        commandHelp(root),
		rootOutput:      root.Output,
		rootDiscovery:   root.RemoteDiscovery,
		rootPassthrough: root.Passthrough,
		schemas:         spec.Command.Schemas,
		configFiles:     allScopedConfigFiles(spec),
		envPrefix:       spec.Command.EnvPrefix,
		version:         version,
	}
	gp.root = genCommand{
		prefix:      gp.rootPascal,
		handler:     lowerFirst(gp.rootPascal) + "Handlers",
		filename:    commandStubFilename(root.Name, "", root.Filename),
		flags:       flagFields(root.inputs()),
		args:        argFields(root.inputs()),
		env:         envFields(root.inputs(), gp.envPrefix),
		config:      configFields(root.inputs()),
		stdinType:   stdinTypeExpr(gp.rootPascal, root.inputs()),
		stdinFormat: stdinFormatExpr(root.inputs()),
		inputs:      []fieldDef{{Field: gp.rootPascal, GoType: gp.rootPascal + "CommandInputs"}},
	}

	absSpec := specPath
	if a, err := filepath.Abs(specPath); err == nil {
		absSpec = filepath.Clean(a)
	}
	base := filepath.Dir(absSpec) // the entry spec is always local; its dir is the base for relative refs
	seen := map[string]bool{absSpec: true}

	tree, err := gp.walk(root.Commands, "", base, moduleRoot, moduleName, seen, composeCtx{})
	if err != nil {
		return nil, err
	}
	gp.tree = tree

	sort.Slice(gp.own, func(i, j int) bool { return gp.own[i].prefix < gp.own[j].prefix })
	sort.Slice(gp.composed, func(i, j int) bool { return gp.composed[i].prefix < gp.composed[j].prefix })
	return gp, nil
}

func (gp *genProgram) walk(cmds []Command, parentPath, base, moduleRoot, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	out := make([]rnode, 0, len(cmds))
	for _, c := range cmds {
		if c.Ref != "" {
			if ctx.composed {
				// Transitive $ref: the direct child already composed this grandchild
				// and exposes handler methods for it, so graft its tree here and
				// delegate to the child (no new import) — see composeNestedRef.
				nodes, err := gp.composeNestedRef(c, parentPath, base, moduleRoot, moduleName, seen, ctx)
				if err != nil {
					return nil, err
				}
				out = append(out, nodes...)
				continue
			}
			node, err := gp.composeRef(c, parentPath, base, moduleRoot, moduleName, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, node)
			continue
		}
		if c.Name == "" {
			return nil, errors.New("command entry has neither a name nor a $ref")
		}

		path := c.Name
		if parentPath != "" {
			path = parentPath + "_" + c.Name
		}
		prefix := gp.rootPascal + toPascalCase(path)

		if ctx.composed {
			rel := strings.TrimPrefix(strings.TrimPrefix(path, ctx.rootPath), "_")
			gp.composed = append(gp.composed, composedCmd{
				prefix:         prefix,
				delegateAlias:  ctx.alias,
				delegateMethod: ctx.childPascal + toPascalCase(rel),
				passthrough:    ctx.passthrough,
			})
		} else {
			gc := genCommand{
				prefix:      prefix,
				handler:     lowerFirst(gp.rootPascal) + toPascalCase(path) + "Handlers",
				filename:    commandStubFilename(gp.rootName, path, c.Filename),
				flags:       flagFields(c.inputs()),
				args:        argFields(c.inputs()),
				env:         envFields(c.inputs(), gp.envPrefix),
				config:      configFields(c.inputs()),
				stdinType:   stdinTypeExpr(prefix, c.inputs()),
				stdinFormat: stdinFormatExpr(c.inputs()),
				inputs:      inputsFields(gp.rootPascal, path),
			}
			if c.Handler != nil {
				// Inline-command passthrough (D-W9.7/.9): still an own command (inputs
				// generated above), but the handler delegates to the package per-node —
				// no subtree cascade, so a child without its own handler: still stubs.
				// Clear the stub filename so a converted command's old stub is pruned
				// (the package owns the handler now), matching inline→$ref conversion.
				alias, importPath := parseAliasPath(c.Handler.Import)
				gp.addImport(alias, importPath)
				gc.passthrough = true
				gc.delegateAlias = alias
				gc.delegateMethod = c.Handler.Convention
				gc.filename = ""
			}
			gp.own = append(gp.own, gc)
		}

		children, err := gp.walk(c.Commands, path, base, moduleRoot, moduleName, seen, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, rnode{
			name:                  c.Name,
			prefix:                prefix,
			aliases:               c.Aliases,
			inputs:                c.inputs(),
			help:                  commandHelp(c),
			output:                c.Output,
			discovery:             c.RemoteDiscovery,
			hidden:                c.Hidden,
			group:                 c.Group,
			deprecated:            c.Deprecated,
			deprecatedIdentifiers: c.DeprecatedIdentifiers,
			passthrough:           c.Passthrough,
			composed:              ctx.composed,
			remotes:               c.RemoteCommands,
			children:              children,
		})
	}
	if err := checkCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

// overlayCommand applies the $ref OVERLAY model (D-W8.2): the `$ref`'d child command
// is the base, and every identity/presentation key the parent author declared on the
// `$ref` node wins over the child's when present (else the child's is kept). This is
// how a parent consuming a child tailors its tree (rename, re-summarize, regroup,
// hide, deprecate) without forking the child. NOT overlaid here: `commands` (merged
// additively at the call site) and the handler-coupled keys (inputs/output/remote_*),
// which a composed command cannot honor — validation rejects those on a `$ref` node.
func overlayCommand(child, parent Command) Command {
	m := child
	if parent.Name != "" {
		m.Name = parent.Name
	}
	if len(parent.Aliases) > 0 {
		m.Aliases = parent.Aliases
	}
	if len(parent.DeprecatedIdentifiers) > 0 {
		m.DeprecatedIdentifiers = parent.DeprecatedIdentifiers
	}
	if parent.Hidden {
		m.Hidden = true
	}
	if parent.Group != "" {
		m.Group = parent.Group
	}
	if parent.Deprecated != "" {
		m.Deprecated = parent.Deprecated
	}
	if parent.Summary != "" {
		m.Summary = parent.Summary
	}
	if parent.Description != "" {
		m.Description = parent.Description
	}
	if parent.Usage != "" {
		m.Usage = parent.Usage
	}
	if parent.Header != "" {
		m.Header = parent.Header
	}
	if parent.Footer != "" {
		m.Footer = parent.Footer
	}
	if parent.Headings != nil {
		m.Headings = parent.Headings
	}
	if len(parent.Examples) > 0 {
		m.Examples = parent.Examples
	}
	if len(parent.ExitStatus) > 0 {
		m.ExitStatus = parent.ExitStatus
	}
	if len(parent.SeeAlso) > 0 {
		m.SeeAlso = parent.SeeAlso
	}
	if parent.Help != "" {
		m.Help = parent.Help
	}
	if parent.Man != "" {
		m.Man = parent.Man
	}
	if parent.Markdown != "" {
		m.Markdown = parent.Markdown
	}
	if parent.Filename != "" {
		m.Filename = parent.Filename
	}
	return m
}

// composeRef loads a `$ref`'d child spec and grafts its command tree as a composed
// subtree, applying the overlay model (parent keys on the `$ref` node win) and merging
// any `commands:` the parent authored next to the `$ref` (additive — the child's own
// commands delegate to the child; the authored siblings are own/new compositions). The
// delegation always targets the child's real handler methods. Transitive $refs (a
// composed child that itself $refs) are handled by composeNestedRef during the walk.
func (gp *genProgram) composeRef(c Command, parentPath, base, moduleRoot, moduleName string, seen map[string]bool) (rnode, error) {
	locator, err := locateRef(base, c.Ref)
	if err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if seen[locator] {
		return rnode{}, fmt.Errorf("cyclic $ref: %q", c.Ref)
	}
	seen[locator] = true
	defer delete(seen, locator)

	rr, err := loadRef(locator, moduleName, moduleRoot)
	if err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	// Cross-tree version guard (W8/D-W8.7): every composed spec MUST declare a `version`
	// that exactly matches the running version, so the whole composed tree provably
	// shares one version. (Skipped only when gp.version is unset, e.g. tests.)
	if err := checkComposedSchemaVersion(c.Ref, rr.spec.Version, gp.version); err != nil {
		return rnode{}, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	childRoot := rr.spec.Command
	if childRoot.Name == "" {
		return rnode{}, fmt.Errorf("composed spec %q has no name", c.Ref)
	}

	// Resolve the handler source for the composed subtree (W9). An explicit `handler:`
	// (the package-import passthrough) wins: handlers come from the declared package via
	// alias.<Convention>(). Otherwise a local/mod:// child auto-delegates to its own
	// generated cli (alias.Handlers().<Name>()). A git/raw spec is not a Go package, so
	// without a `handler:` there is nothing to delegate to — that is an error.
	var alias, delegateRoot string
	var passthrough bool
	switch {
	case c.Handler != nil:
		a, p := parseAliasPath(c.Handler.Import)
		alias, delegateRoot, passthrough = a, c.Handler.Convention, true
		gp.addImport(a, p)
	case isExternalLocator(locator):
		return rnode{}, fmt.Errorf("compose %q: an external git/raw $ref needs a `handler:` — a fetched spec is not an importable Go package, so declare handler.import + handler.convention to source its handlers", c.Ref)
	default:
		alias = identAlias(childRoot.Name)
		delegateRoot = toPascalCase(childRoot.Name)
		gp.addImport(alias, childCliImport(rr.dir, rr.module))
	}

	// Overlay the parent's $ref-node keys onto the child (parent wins when present).
	// The grafted command is named after the child's root unless the ref overrides it;
	// the override changes only the parent-side name/path, never the delegation target.
	merged := overlayCommand(childRoot, c)
	graftName := merged.Name
	composeRootPath := graftName
	if parentPath != "" {
		composeRootPath = parentPath + "_" + graftName
	}
	prefix := gp.rootPascal + toPascalCase(composeRootPath)

	gp.composed = append(gp.composed, composedCmd{prefix: prefix, delegateAlias: alias, delegateMethod: delegateRoot, passthrough: passthrough})

	ctx := composeCtx{composed: true, rootPath: composeRootPath, childPascal: delegateRoot, alias: alias, passthrough: passthrough}
	children, err := gp.walk(childRoot.Commands, composeRootPath, rr.childBase, moduleRoot, moduleName, seen, ctx)
	if err != nil {
		return rnode{}, err
	}
	// Merge the `commands:` the parent authored next to the `$ref` (the croot/c3 case):
	// they are NOT the child's — they resolve against the PARENT base and are own
	// commands / new compositions (the outer, non-composed context), grafted alongside
	// the child's own subtree. A name/alias collision across the merged set is an error.
	if len(c.Commands) > 0 {
		authored, err := gp.walk(c.Commands, composeRootPath, base, moduleRoot, moduleName, seen, composeCtx{})
		if err != nil {
			return rnode{}, err
		}
		children = append(children, authored...)
		if err := checkCollisions(children); err != nil {
			return rnode{}, err
		}
	}
	return rnode{name: graftName, prefix: prefix, aliases: merged.Aliases, inputs: childRoot.inputs(), help: commandHelp(merged), hidden: merged.Hidden, group: merged.Group, deprecated: merged.Deprecated, deprecatedIdentifiers: merged.DeprecatedIdentifiers, composed: true, children: children}, nil
}

// composeNestedRef handles a `$ref` encountered *inside* an already-composed subtree
// (a transitive ref: parent → child → grandchild). The direct child already composed
// the grandchild and exposes handler methods for it, so the parent does not import the
// grandchild's cli — it grafts the grandchild's command tree here and lets the normal
// composed-walk delegate each node to the direct child (delegateMethod =
// ctx.childPascal + the node's relative path, which matches the child's method names).
// Parent overlay keys win, mirroring composeRef; any `commands:` authored next to the
// nested `$ref` are merged additively (they resolve against the spec that holds the
// nested ref and delegate to the same direct child, which already composed them).
func (gp *genProgram) composeNestedRef(c Command, parentPath, base, moduleRoot, moduleName string, seen map[string]bool, ctx composeCtx) ([]rnode, error) {
	locator, err := locateRef(base, c.Ref)
	if err != nil {
		return nil, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if seen[locator] {
		return nil, fmt.Errorf("cyclic $ref: %q", c.Ref)
	}
	seen[locator] = true
	defer delete(seen, locator)

	rr, err := loadRef(locator, moduleName, moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if err := checkComposedSchemaVersion(c.Ref, rr.spec.Version, gp.version); err != nil {
		return nil, fmt.Errorf("compose %q: %w", c.Ref, err)
	}
	if isExternalLocator(locator) {
		return nil, fmt.Errorf("compose %q: external git/raw $ref composition is not yet supported — it needs a handler source (the W9 passthrough); use a local or mod:// $ref", c.Ref)
	}
	gc := rr.spec.Command
	if gc.Name == "" {
		return nil, fmt.Errorf("composed spec %q has no name", c.Ref)
	}

	// Graft the grandchild as a named command in the current composed subtree: overlay
	// the parent's $ref-node keys, then run it (and its descendants) through the normal
	// walk so the standard composed delegation applies and the rnodes are marked composed
	// (no types emitted here). Its own subtree resolves against the grandchild's dir.
	synth := overlayCommand(gc, c)
	synth.Ref = ""
	synth.Commands = gc.Commands // overlayCommand left Commands == gc's; siblings merge below
	nodes, err := gp.walk([]Command{synth}, parentPath, rr.childBase, moduleRoot, moduleName, seen, ctx)
	if err != nil {
		return nil, err
	}
	// Merge `commands:` authored next to the nested `$ref`. Unlike the grandchild's own
	// subtree, these resolve against THIS spec's base and delegate to the same direct
	// child (the current composed ctx). Graft them as children of the grandchild.
	if len(c.Commands) > 0 && len(nodes) == 1 {
		siblingParent := synth.Name
		if parentPath != "" {
			siblingParent = parentPath + "_" + synth.Name
		}
		authored, err := gp.walk(c.Commands, siblingParent, base, moduleRoot, moduleName, seen, ctx)
		if err != nil {
			return nil, err
		}
		nodes[0].children = append(nodes[0].children, authored...)
		if err := checkCollisions(nodes[0].children); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (gp *genProgram) addImport(alias, path string) {
	for _, ci := range gp.childImports {
		if ci.Path == path {
			return
		}
	}
	gp.childImports = append(gp.childImports, templateHandlersImport{Alias: alias, Path: path})
}

// childCliImport resolves the import path of a composed child's cmd package — the
// handler package that exposes Handlers() — within the module the child belongs to
// (the consuming module for a local ref, the external module for a mod:// ref). It
// reads the child's conf (in childDir) for the cmd package, falling back to the default
// internal/cmd/<child> convention (named after the child's directory).
func childCliImport(childDir, module string) string {
	if confPath, err := discoverConf(childDir); err == nil {
		cc, err := readConf(confPath)
		if err == nil && cc.Generate != nil && cc.Generate.Packages != nil &&
			cc.Generate.Packages.Cmd != nil && cc.Generate.Packages.Cmd.Package != "" {
			return module + "/" + filepath.ToSlash(cc.Generate.Packages.Cmd.Package)
		}
	}
	return module + "/internal/cmd/" + filepath.Base(childDir)
}

// identAlias derives a valid, reasonably unique Go import alias from a command
// name. Each composed child's cli package is named after the child (internal/cmd/
// <child>), so an explicit alias keeps the rollup's references unambiguous and
// clear of the rotini runtime package (also a bare package name).
func identAlias(name string) string {
	return lowerFirst(toPascalCase(name)) + "cli"
}

// checkCollisions errors when sibling commands share a name or alias.
func checkCollisions(nodes []rnode) error {
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, id := range append([]string{n.name}, n.aliases...) {
			if seen[id] {
				return fmt.Errorf("duplicate command name or alias %q among siblings", id)
			}
			seen[id] = true
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Doc features — help / man / completion page generation.
// ─────────────────────────────────────────────────────────────────────────────.

// helpTemplateName / manTemplateName are the editable, seed-once doc templates
// living in the feature dir (the only user-owned files there). Pruning always
// keeps them.
const (
	helpTemplateName     = "help.txt.tmpl"
	manTemplateName      = "man.txt.tmpl"
	markdownTemplateName = "markdown.md.tmpl"
)

// docFeature describes one doc-rendered codegen feature (help, man). Both share
// the doc-data pipeline (buildHelpData → renderDocText) and differ only in their
// file suffix, embed-var/resolver names, the editable template, and which
// per-command verbatim spec string escapes the render.
//
// All enabled features default to ONE shared embed dir, so each feature's
// output files must be distinguishable by name alone: filePrefix is a
// feature-unique prefix ("help_" / "man_" / "completion_") that also groups
// each feature's files together in directory listings, ext the file suffix,
// and pruning only considers files matching both — features sharing a dir can
// never prune (or collide with) each other's files.
type docFeature struct {
	name       string               // feature key, e.g. "help"
	noun       string               // word used in the resolver doc comment / error, e.g. "help"
	varPrefix  string               // embed-var prefix, e.g. "Help" → HelpRotiniGenerate
	resolver   string               // resolver func name, e.g. "Help"
	ext        string               // output file suffix, e.g. ".txt"
	filePrefix string               // feature-unique output file prefix, e.g. "help_"
	tmplFile   string               // editable template file name in the feature dir ("" = none)
	embedded   string               // embedded default template text, from renderer.go ("" = none)
	verbatim   func(cmdHelp) string // the per-command verbatim escape for this feature (nil = none)
	perShell   bool                 // completion: keyed by shell name, not command path
	strip      bool                 // strip spec-authored ANSI from the output (man/markdown — never help)
}

var (
	helpFeatureDesc = docFeature{
		name: "help", noun: "help", varPrefix: "Help", resolver: "Help",
		ext: ".txt", filePrefix: "help_", tmplFile: helpTemplateName, embedded: templateHelp,
		verbatim: func(h cmdHelp) string { return h.Help },
		// help is the TERMINAL surface — spec-authored styling is kept.
	}
	manFeatureDesc = docFeature{
		name: "man", noun: "man", varPrefix: "Man", resolver: "Man",
		ext: ".txt", filePrefix: "man_", tmplFile: manTemplateName, embedded: templateMan,
		verbatim: func(h cmdHelp) string { return h.Man },
		strip:    true, // a roff/plain man page carries no legitimate SGR (E6-S1)
	}
	markdownFeatureDesc = docFeature{
		name: "markdown", noun: "markdown", varPrefix: "Markdown", resolver: "Markdown",
		ext: ".md", filePrefix: "markdown_", tmplFile: markdownTemplateName, embedded: templateMarkdown,
		verbatim: func(h cmdHelp) string { return h.Markdown },
		strip:    true, // a markdown file carries no legitimate SGR (E6-S1)
	}
	// completionFeatureDesc is the group's exception: keyed by shell, no doc-data,
	// no template, no verbatim. Scripts come from completionScript at codegen.
	completionFeatureDesc = docFeature{
		name: "completion", noun: "completion", varPrefix: "Completion", resolver: "Completion",
		ext: ".txt", filePrefix: "completion_", perShell: true,
	}
)

// completionShells are the shells rotini generates completion scripts for, in a
// deterministic order (matches completionScript's supported set).
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

// helpNode is one command's help wiring: the embed var/resolver identity plus
// everything needed to produce its .txt. One is produced per command (root +
// every own and composed sub-command). The .txt is produced one of two ways,
// selected by whether the command's verbatim `help` string is set: non-empty →
// write it verbatim; empty → render `data` through the template.
type helpNode struct {
	prefix   string           // PascalCase command prefix; the embed var is "Help"+prefix
	file     string           // .txt file name within the help dir
	paths    []string         // resolver case values (name/alias permutations); root = [""]
	name     string           // the command's invocation name, e.g. "rotini generate"
	verbatim string           // command.help — the exact page; "" means render from data
	data     templateHelpData // rendering inputs (used when verbatim == "")
}

// cmdHelp bundles a command's resolved help-presentation fields, which live
// directly on the spec's command (and root). Help, when non-empty, is the exact
// verbatim page; otherwise the page is rendered from the structured fields.
type cmdHelp struct {
	Summary     string
	Description string
	Usage       string
	Header      string
	Footer      string
	Headings    *HelpHeadings
	Examples    []string
	ExitStatus  []ExitStatusEntry // command.exit_status (man EXIT STATUS section)
	SeeAlso     []string          // command.see_also (man SEE ALSO section)
	Help        string            // verbatim help page (command.help)
	Man         string            // verbatim man page (command.man)
	Markdown    string            // verbatim markdown reference page (command.markdown)
}

// commandHelp gathers the flattened doc-fields off a command (root or sub).
func commandHelp(c Command) cmdHelp {
	return cmdHelp{
		Summary: c.Summary, Description: c.Description, Usage: c.Usage,
		Header: c.Header, Footer: c.Footer, Headings: c.Headings,
		Examples: c.Examples, ExitStatus: c.ExitStatus, SeeAlso: c.SeeAlso,
		Help: c.Help, Man: c.Man, Markdown: c.Markdown,
	}
}

// flattenFeature produces a node per command for one doc feature across the whole
// resolved tree: the root first, then every sub-command in tree order. Each node
// carries its verbatim page (the feature's spec escape, when set) and its built
// doc-data (templateHelpData, used when no verbatim page is given). The file
// extension is the feature's; the doc-data is identical across features.
func flattenFeature(gp *genProgram, feat docFeature) []helpNode {
	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     feat.filePrefix + gp.rootName + feat.ext,
		paths:    []string{""},
		name:     gp.rootName,
		verbatim: feat.verbatim(gp.rootHelp),
		data:     buildHelpData(gp.rootName, gp.rootHelp, gp.rootInputs, gp.tree, gp.rootRemotes, nil, gp.envPrefix),
	}}

	// cascading carries the cascading flags accumulated from a node's ancestors
	// (the root's own cascading flags seed the root's children, and so on down).
	var walk func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow)
	walk = func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootName + " " + strings.Join(childNames, " ")
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     feat.filePrefix + gp.rootName + "_" + strings.Join(childNames, "_") + feat.ext,
				paths:    permute(childChain),
				name:     invocation,
				verbatim: feat.verbatim(n.help),
				data:     buildHelpData(invocation, n.help, n.inputs, n.children, n.remotes, cascading, gp.envPrefix),
			})
			childCascading := append(append([]templateDocFlagRow{}, cascading...), cascadingFlagsOf(n.inputs)...)
			walk(n.children, childChain, childNames, childCascading)
		}
	}
	walk(gp.tree, nil, nil, cascadingFlagsOf(gp.rootInputs))
	return out
}

// completionNodes produces one node per supported shell for the completion
// feature: keyed by shell name (the resolver case), file
// completion_<shell>.txt, embed var Completion<Shell>. No doc-data or
// verbatim — the script comes from writeCompletionFiles.
func completionNodes() []helpNode {
	out := make([]helpNode, 0, len(completionShells))
	for _, sh := range completionShells {
		out = append(out, helpNode{
			prefix: toPascalCase(sh),
			file:   completionFeatureDesc.filePrefix + sh + completionFeatureDesc.ext,
			paths:  []string{sh},
			name:   sh,
		})
	}
	return out
}

// resolveHeadings applies the section-heading defaults, overriding with any set
// in the spec.
func resolveHeadings(h cmdHelp) templateDocHeadings {
	// Defaults carry the trailing ":" so an override is rendered verbatim — a spec
	// author can drop or restyle the colon (the template adds nothing).
	hd := templateDocHeadings{Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:", Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:", Cascading: "Global Flags:", Examples: "Examples:"}
	if h.Headings == nil {
		return hd
	}
	override := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	o := h.Headings
	override(&hd.Usage, o.Usage)
	override(&hd.Commands, o.Commands)
	override(&hd.Arguments, o.Arguments)
	override(&hd.Flags, o.Flags)
	override(&hd.Environment, o.Environment)
	override(&hd.Configuration, o.Configuration)
	override(&hd.Cascading, o.Cascading)
	override(&hd.Examples, o.Examples)
	return hd
}

// buildHelpData assembles the template context for one command from its help
// fields, inputs, direct children, and remote sub-commands. Hidden
// children/inputs are excluded; remotes join the Commands list (they dispatch
// like any sub-command).
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode, remotes []RemoteCommandSpec, ancestorCascading []templateDocFlagRow, envPrefix string) templateHelpData {
	d := templateHelpData{
		Invocation:  invocation,
		Headings:    resolveHeadings(h),
		Header:      h.Header,
		Summary:     h.Summary,
		Description: h.Description,
		Usage:       h.Usage,
		Footer:      h.Footer,
		Cascading:   ancestorCascading,
		Examples:    h.Examples,
		SeeAlso:     h.SeeAlso,
	}
	for _, e := range h.ExitStatus {
		d.ExitStatus = append(d.ExitStatus, templateDocExitRow(e))
	}
	var cmds []templateDocCommandRow
	for _, c := range children {
		if c.hidden {
			continue
		}
		cmds = append(cmds, templateDocCommandRow{
			Name:       c.name,
			Summary:    c.help.Summary,
			Aliases:    c.aliases,
			Group:      c.group,
			Deprecated: c.deprecated,
		})
	}
	for _, r := range remotes {
		cmds = append(cmds, templateDocCommandRow{
			Name:    r.Name,
			Summary: r.Summary,
			Aliases: r.Aliases,
		})
	}
	d.CommandGroups = groupCommands(cmds)
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			d.Arguments = append(d.Arguments, templateDocArgumentRow{
				Name:       a.Name,
				Summary:    a.Summary,
				Required:   a.Schema != nil && a.Schema.Required,
				Variadic:   isVariadicSchema(a.Schema),
				Default:    schemaDefaultString(a.Schema),
				Enum:       enumOf(a.Schema),
				Deprecated: a.Deprecated,
			})
		}
		for _, f := range inputs.Flags {
			if f.Hidden {
				continue
			}
			d.Flags = append(d.Flags, flagRow(f))
		}
		for _, e := range inputs.Env {
			if e.Hidden {
				continue
			}
			d.Environment = append(d.Environment, templateDocEnvRow{
				Var:        envVarLabel(e, envPrefix),
				Summary:    e.Summary,
				Type:       flagDisplayType(e.Schema),
				Required:   e.Schema != nil && e.Schema.Required,
				Default:    schemaDefaultString(e.Schema),
				Enum:       enumOf(e.Schema),
				Deprecated: e.Deprecated,
			})
		}
		for _, c := range inputs.Config {
			if c.Hidden {
				continue
			}
			d.Configuration = append(d.Configuration, templateDocConfigRow{
				Name:       c.Name,
				Location:   configLocation(c),
				Summary:    c.Summary,
				Type:       flagDisplayType(c.Schema),
				Required:   c.Schema != nil && c.Schema.Required,
				Default:    schemaDefaultString(c.Schema),
				Enum:       enumOf(c.Schema),
				Deprecated: c.Deprecated,
			})
		}
	}
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children) || len(remotes) > 0)
	return d
}

// envVarLabel is the environment variable an env input reads: its explicit
// schema.variable, else the snake-upper form of its logical name (mirroring the
// binder's default key→env-var derivation, e.g. "apiKey" → "API_KEY").
func envVarLabel(e EnvInput, envPrefix string) string {
	if e.Schema != nil && e.Schema.Variable != "" {
		return e.Schema.Variable // explicit: exempt from env_prefix
	}
	if envPrefix != "" {
		return envPrefix + "_" + snakeUpper(e.Name)
	}
	return snakeUpper(e.Name)
}

// configLocation is where a config input is read from, for display: "<file>.<key>"
// when a source file is named, the bare key when an explicit key differs from the
// logical name, or "" when the input reads from its own name (nothing to add).
func configLocation(c ConfigInput) string {
	if c.Schema == nil {
		return ""
	}
	key := c.Schema.Key
	switch {
	case c.Schema.File != "":
		if key == "" {
			key = c.Name
		}
		return c.Schema.File + "." + key
	case key != "":
		return key
	default:
		return ""
	}
}

// groupCommands buckets command rows by their Group, preserving the order in which each
// group first appears in the declared command list. Ungrouped rows (Group == "") form a
// bucket with Title "" — each template heads it with its own default. When no row
// declares a group, the result is a single Title-"" bucket holding every command in
// order, so a non-grouped command list renders byte-identically to before.
func groupCommands(rows []templateDocCommandRow) []templateDocCommandGroup {
	if len(rows) == 0 {
		return nil
	}
	idx := map[string]int{}
	var groups []templateDocCommandGroup
	for _, r := range rows {
		i, ok := idx[r.Group]
		if !ok {
			i = len(groups)
			idx[r.Group] = i
			groups = append(groups, templateDocCommandGroup{Title: r.Group})
		}
		groups[i].Commands = append(groups[i].Commands, r)
	}
	return groups
}

// snakeUpper converts a logical name to the conventional SCREAMING_SNAKE_CASE env-var
// form: word boundaries are '-'/'_'/' ' and lower→upper case transitions.
func snakeUpper(name string) string {
	var b strings.Builder
	var prev rune
	for i, r := range name {
		switch {
		case r == '-' || r == '_' || r == ' ':
			b.WriteByte('_')
		case i > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			b.WriteByte('_')
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
		prev = r
	}
	return b.String()
}

// flagRow builds the help-row for a single flag (shared by a command's own Flags
// section and the Cascading section it contributes to its descendants).
func flagRow(f FlagInput) templateDocFlagRow {
	return templateDocFlagRow{
		Identifiers: flagIdentifiers(f),
		Summary:     f.Summary,
		Type:        flagDisplayType(f.Schema),
		Required:    f.Schema != nil && f.Schema.Required,
		Default:     schemaDefaultString(f.Schema),
		Enum:        enumOf(f.Schema),
		Deprecated:  f.Deprecated,
	}
}

// cascadingFlagsOf returns the help-rows for a command's own flags marked
// cascading: true (and not hidden) — the flags it advertises on its descendants'
// pages. Order follows declaration order, matching the Flags section.
func cascadingFlagsOf(inputs *Inputs) []templateDocFlagRow {
	if inputs == nil {
		return nil
	}
	var rows []templateDocFlagRow
	for _, f := range inputs.Flags {
		if f.Hidden || !f.Cascading {
			continue
		}
		rows = append(rows, flagRow(f))
	}
	return rows
}

// deriveUsage builds the default usage line: invocation, a <command> slot when
// the node has visible children, each visible argument decorated, then [flags]
// when the node has visible flags.
func deriveUsage(invocation string, inputs *Inputs, hasChildren bool) string {
	var b strings.Builder
	b.WriteString(invocation)
	if hasChildren {
		b.WriteString(" <command>")
	}
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			name := a.Name
			if a.Schema != nil && a.Schema.Placeholder != "" {
				name = a.Schema.Placeholder // the <>/[]/… decoration still applies
			}
			if isVariadicSchema(a.Schema) {
				name += "..."
			}
			if a.Schema != nil && a.Schema.Required {
				b.WriteString(" <" + name + ">")
			} else {
				b.WriteString(" [" + name + "]")
			}
		}
	}
	if hasVisibleFlags(inputs) {
		b.WriteString(" [flags]")
	}
	return b.String()
}

func isVariadicSchema(schema *InputSchema) bool {
	return strings.HasPrefix(getSchemaType(schema), "[]")
}

// flagDisplayType returns the value token shown after a flag's identifiers in
// help/man: the declared placeholder when set, else the resolved type; "" for
// bool flags (which take no value).
func flagDisplayType(schema *InputSchema) string {
	t := getSchemaType(schema)
	if t == "bool" || (schema != nil && schema.Type == "count") {
		return "" // presence flags take no value token
	}
	if schema != nil && schema.Placeholder != "" {
		return schema.Placeholder
	}
	return t
}

func schemaDefaultString(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	return defaultString(schema.Default)
}

func enumOf(schema *InputSchema) []string {
	if schema == nil {
		return nil
	}
	return schema.Enum
}

// flagIdentifiers returns a flag's CLI identifiers, deriving "--<name>" when none
// are declared (mirrors flagDefsLiteral).
func flagIdentifiers(f FlagInput) []string {
	if len(f.Identifiers) > 0 {
		return f.Identifiers
	}
	return []string{"--" + strings.ReplaceAll(f.Name, "_", "-")}
}

func hasVisibleChildren(children []rnode) bool {
	for _, c := range children {
		if !c.hidden {
			return true
		}
	}
	return false
}

func hasVisibleFlags(inputs *Inputs) bool {
	if inputs == nil {
		return false
	}
	for _, f := range inputs.Flags {
		if !f.Hidden {
			return true
		}
	}
	return false
}

// permute returns every space-joined path through the chain of per-segment
// identifier sets (name + aliases), so the resolver matches an aliased path. An
// empty chain (the root) yields the single empty path.
func permute(chain [][]string) []string {
	out := []string{""}
	for _, seg := range chain {
		var next []string
		for _, prefix := range out {
			for _, id := range seg {
				if prefix == "" {
					next = append(next, id)
				} else {
					next = append(next, prefix+" "+id)
				}
			}
		}
		out = next
	}
	return out
}

// buildFeatureFramework turns a feature's nodes into the embed vars + resolver
// cases the framework template emits (one resolver per feature).
func buildFeatureFramework(nodes []helpNode, dir string, feat docFeature, embed bool, contents []string) templateFeature {
	h := templateFeature{Resolver: feat.resolver, Noun: feat.noun, PerShell: feat.perShell}
	for i, hn := range nodes {
		name := feat.varPrefix + hn.prefix
		v := templateFeatureVar{Name: name}
		if embed {
			// A "." dir (the feature dir IS the cmdgen package dir) embeds the bare
			// file name — "./x" is not a valid //go:embed pattern.
			embedPath := hn.file
			if dir != "" && dir != "." {
				embedPath = dir + "/" + hn.file
			}
			v.Embed = embedPath
		} else {
			// Inline: the content rides as a Go string literal in the .go — no
			// file, no //go:embed. strconv.Quote handles backticks (markdown) and
			// ANSI/ESC bytes (styled help) that a raw-string literal could not.
			v.Literal = strconv.Quote(contents[i])
		}
		h.Vars = append(h.Vars, v)
		quoted := make([]string, len(hn.paths))
		for j, p := range hn.paths {
			quoted[j] = strconv.Quote(p)
		}
		h.Cases = append(h.Cases, templateFeatureCase{PathsLiteral: strings.Join(quoted, ", "), Var: name})
	}
	return h
}

// writeFeatureFiles produces each command's rendered page for one feature under
// its dir in the framework package. When the command's verbatim spec string for
// the feature is set, that string is written byte-exact; otherwise the page is
// rendered from the shared doc-data via the feature's template. Either way the
// file is rotini-managed — (re)written every pass, skipped when already identical —
// like rotini.go and handlers.go. The template is loaded (seeding the editable
// default when missing) only when at least one command renders.
func docFeatureContents(featDir string, nodes []helpNode, feat docFeature, seedTemplate bool) ([]string, error) {
	renders := false
	for _, hn := range nodes {
		if hn.verbatim == "" {
			renders = true
			break
		}
	}

	var tmpl *template.Template
	if renders {
		var err error
		if seedTemplate {
			// template:true — seed the editable default to disk (when missing) and
			// render from it, so the author can customize.
			if err = os.MkdirAll(featDir, 0o755); err != nil {
				return nil, fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
			}
			tmpl, err = loadFeatureTemplate(featDir, feat)
		} else {
			// template:false — render from rotini's built-in default in memory; no
			// editable template is written.
			tmpl, err = parseDocTemplate(feat.tmplFile, feat.embedded)
		}
		if err != nil {
			return nil, err
		}
	}

	contents := make([]string, len(nodes))
	for i, hn := range nodes {
		if hn.verbatim != "" {
			// Verbatim: exactly what the spec supplied — byte-for-byte (the author
			// controls trailing newlines via YAML). A strip feature (man/markdown)
			// still removes any ANSI: a verbatim page is no more a terminal surface
			// than a rendered one.
			contents[i] = stripForFeature(feat, hn.verbatim)
			continue
		}
		rendered, err := renderDocText(tmpl, hn.data)
		if err != nil {
			return nil, fmt.Errorf("render %s for %q: %w", feat.name, hn.name, err)
		}
		contents[i] = stripForFeature(feat, rendered)
	}
	return contents, nil
}

// completionContents computes each shell's completion script (no template, no
// ANSI strip — a script is not a styled surface), parallel to nodes.
func completionContents(prog string, nodes []helpNode) ([]string, error) {
	contents := make([]string, len(nodes))
	for i, n := range nodes {
		script, err := completionScript(prog, n.name)
		if err != nil {
			return nil, fmt.Errorf("generate %s completion: %w", n.name, err)
		}
		contents[i] = script
	}
	return contents, nil
}

// writeFeatureOutputs writes each node's precomputed content to its file under
// featDir — used ONLY in embed mode (//go:embed). Inline features write no
// output files: their content lives in the generated .go as a string literal.
func writeFeatureOutputs(featDir string, nodes []helpNode, contents []string, feat docFeature) error {
	if featDir == "" {
		return fmt.Errorf("generate.features.%s.dir must not be empty", feat.name)
	}
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		return fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
	}
	for i, hn := range nodes {
		if err := writeIfChanged(filepath.Join(featDir, hn.file), contents[i]); err != nil {
			return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
		}
	}
	return nil
}

// stripForFeature removes spec-authored ANSI styling from a feature's output
// when the feature is not a terminal surface (man, markdown — E6-S1). Help
// keeps its styling; this returns text unchanged for non-strip features.
func stripForFeature(feat docFeature, text string) string {
	if !feat.strip {
		return text
	}
	return rotini.Strip(text)
}

// loadFeatureTemplate reads the feature dir's editable template, seeding it from
// the embedded default when missing, and parses it with the shared FuncMap.
func loadFeatureTemplate(featDir string, feat docFeature) (*template.Template, error) {
	path := filepath.Join(featDir, feat.tmplFile)
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if werr := os.WriteFile(path, []byte(feat.embedded), 0o644); werr != nil {
			return nil, fmt.Errorf("seed %s template %s: %w", feat.name, path, werr)
		}
		src = []byte(feat.embedded)
	} else if err != nil {
		return nil, fmt.Errorf("read %s template %s: %w", feat.name, path, err)
	}
	tmpl, err := parseDocTemplate(feat.tmplFile, string(src))
	if err != nil {
		return nil, fmt.Errorf("%s template %s: %w", feat.name, path, err)
	}
	return tmpl, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Output & stdin schema types.
// ─────────────────────────────────────────────────────────────────────────────.

// outputRootSentinel is the throwaway root type GenerateGo always emits for the
// assembled output-types document; it carries no data and is stripped, leaving
// only the document-level named schemas and the per-command <Prefix>Output types.
const outputRootSentinel = "rotiniGeneratedOutputsRoot"

// buildOutputTypes generates the Go type declarations for a program's output
// types and named schemas as a formatted source fragment (no package clause, no
// root type) ready to inject into the framework file. It returns "" when the
// program declares no schemas and no command outputs.
//
// Every document-level schema becomes a named type, and every command (root + own
// sub-commands) that declares `output` gets a "<Prefix>Output" type — an alias-like
// named type when the output is a bare `$ref`, or a struct for an inline shape.
// Generation reuses jsonschema.GenerateGo (the same engine behind the spec/conf
// types), so refs, nesting, arrays, and allOf embedding all work.
func buildOutputTypes(gp *genProgram, pkg string) (string, error) {
	defs := collectOutputDefs(gp)
	if len(defs) == 0 {
		return "", nil
	}
	doc, err := json.Marshal(map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"type":        "object",
		"definitions": defs,
	})
	if err != nil {
		return "", fmt.Errorf("marshal output schema document: %w", err)
	}
	src, err := jsonschema.GenerateGo(doc,
		jsonschema.WithGoPackage(pkg),
		jsonschema.WithGoRootType(outputRootSentinel))
	if err != nil {
		return "", fmt.Errorf("generate output types: %w", err)
	}
	return stripGenerated(string(src), outputRootSentinel), nil
}

// eachOwnNode visits every non-composed command node in the resolved tree
// depth-first (pre-order), skipping composed subtrees entirely — their output and
// stdin types live in the child's cligen. Shared by the output- and stdin-type
// collectors.
func eachOwnNode(nodes []rnode, visit func(n *rnode)) {
	for i := range nodes {
		if nodes[i].composed {
			continue
		}
		visit(&nodes[i])
		eachOwnNode(nodes[i].children, visit)
	}
}

// collectOutputDefs assembles the JSON-schema `definitions` for the output-types
// document: each document-level named schema, plus one "<Prefix>Output" per
// command that declares an output. Refs are rewritten from the spec's
// "#/schemas/" space to the document's "#/definitions/" space.
func collectOutputDefs(gp *genProgram) map[string]any {
	defs := map[string]any{}
	for name, sch := range gp.schemas {
		defs[name] = schemaToDoc(sch)
	}
	add := func(prefix string, out *Schema) {
		if out != nil {
			defs[prefix+"Output"] = schemaToDoc(*out)
		}
	}
	// A command's stdin payload type "<Prefix>Stdin" comes from the schema-shape of
	// its stdin InputSchema (only the BaseSchema part — required/default/etc. are
	// input metadata, not JSON-schema type structure).
	addStdin := func(prefix string, in *Inputs) {
		if in != nil && in.Stdin != nil && in.Stdin.Schema != nil {
			defs[prefix+"Stdin"] = schemaToDoc(Schema{BaseSchema: in.Stdin.Schema.BaseSchema})
		}
	}
	add(gp.rootPascal, gp.rootOutput)
	addStdin(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) {
		add(n.prefix, n.output)
		addStdin(n.prefix, n.inputs)
	})
	return defs
}

// pathFromClaim accumulates the config_source inputs claiming one
// configuration_files entry: a flag's logical name and/or an env input's
// variable.
type pathFromClaim struct {
	flag string
	env  string
}

// collectPathFrom maps each configuration_files name to the inputs that supply
// its path (spec config_source), across the whole command tree: a flag claims
// by logical name; an env input by its variable (explicit `variable:`, else
// the SNAKE_UPPER projection of its name). Validation guarantees single
// claims per channel and that the named entry exists.
func collectPathFrom(gp *genProgram) map[string]pathFromClaim {
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

// contractComment is the TextUnmarshaler nudge emitted as a trailing comment
// on flag/argument fields whose type comes from an explicit spec `import:` —
// the contract is enforced by the argv coerce path, and this puts it where the
// user reads their own generated code. Builtin aliases (duration/time) have
// their own parsing and get no comment; env/config fields decode via recon, a
// different path, so they get none either.
func contractComment(schema *InputSchema) string {
	if schema == nil || strings.TrimSpace(schema.Import) == "" {
		return ""
	}
	return "// parsed via its encoding.TextUnmarshaler (see the spec schema's `type` docs)"
}

// envVarName is an env input's environment variable: the explicit `variable:`
// when declared, else the SNAKE_UPPER projection of its name (recon's default).
func envVarName(e EnvInput, envPrefix string) string {
	if v := envVarOf(e.Schema); v != "" {
		return v // explicit variable: exempt from env_prefix — already exact
	}
	derived := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(e.Name))
	if envPrefix != "" {
		return envPrefix + "_" + derived
	}
	return derived
}

// collectStdinSchemas builds the per-command stdin validation schemas for BindMeta:
// each non-composed command that declares a stdin payload maps its "<Prefix>Stdin"
// type name to a self-contained JSON Schema (the payload schema, plus the document's
// named schemas as definitions so any "#/schemas/X" refs resolve). The binder
// validates the decoded payload against it. Returns nil when no command has stdin.
func collectStdinSchemas(gp *genProgram) map[string]string {
	out := map[string]string{}
	add := func(prefix string, in *Inputs) {
		if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
			return
		}
		if js := stdinValidationSchema(in.Stdin.Schema, gp.schemas); js != "" {
			out[prefix+"Stdin"] = js
		}
	}
	add(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.prefix, n.inputs) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// stdinValidationSchema renders the stdin payload's load-time validation
// schema. It uses only the schema-shape of the InputSchema (the BaseSchema),
// matching the generated type.
func stdinValidationSchema(stdin *InputSchema, docSchemas map[string]Schema) string {
	return validationSchema(Schema{BaseSchema: stdin.BaseSchema}, docSchemas)
}

// validationSchema renders a self-contained JSON Schema (as a JSON string) for
// load-time document validation — the stdin payload and configuration_files
// entries share it: the declared type/properties/constraints, plus the
// document's named schemas as `definitions` (so any "#/schemas/X" refs
// resolve).
func validationSchema(schema Schema, docSchemas map[string]Schema) string {
	body, ok := schemaToDoc(schema).(map[string]any)
	if !ok {
		return ""
	}
	body["$schema"] = "http://json-schema.org/draft-07/schema#"
	if len(docSchemas) > 0 {
		defs := map[string]any{}
		for name, s := range docSchemas {
			defs[name] = schemaToDoc(s)
		}
		body["definitions"] = defs
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(raw)
}

// schemaToDoc marshals a spec Schema to a generic JSON-schema value and rewrites
// its "#/schemas/" refs to "#/definitions/".
func schemaToDoc(s Schema) any {
	raw, err := json.Marshal(s)
	if err != nil {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	rewriteSchemaRefs(v)
	return v
}

// rewriteSchemaRefs deep-walks v, rewriting every {"$ref": "#/schemas/X"} to
// "#/definitions/X" so the assembled document (which uses `definitions`) resolves.
func rewriteSchemaRefs(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					t[k] = strings.Replace(s, "#/schemas/", "#/definitions/", 1)
				}
				continue
			}
			rewriteSchemaRefs(val)
		}
	case []any:
		for _, e := range t {
			rewriteSchemaRefs(e)
		}
	}
}

// stripGenerated reduces a generated Go file to its type declarations: it drops
// the leading "// Code generated …" banner and the "package …" clause, then
// removes the throwaway sentinel root type. The result is gofmt-clean type decls
// the framework template injects and the whole file is re-formatted.
func stripGenerated(src, sentinel string) string {
	// Header banner, package clause, then the body are blank-line separated.
	if parts := strings.SplitN(src, "\n\n", 3); len(parts) == 3 {
		src = parts[2]
	}
	src = strings.ReplaceAll(src, "type "+sentinel+" map[string]any\n", "")
	return strings.TrimSpace(src)
}

// ─────────────────────────────────────────────────────────────────────────────
// Shell completion scripts (bash / zsh / fish / powershell).
// ─────────────────────────────────────────────────────────────────────────────.

// completionScript returns a shell completion script for prog (the installed
// binary name) and shell. The script delegates to the binary's hidden completion
// entrypoint (the runtime's __complete intercept), so completions always reflect
// the live command tree. Supported shells: bash, zsh, fish, powershell.
//
// It is used at codegen time: when the completion feature is enabled, the
// generator renders one script per supported shell and embeds it in the cligen
// package (the runtime then serves the embedded script, never calling this).
func completionScript(prog, shell string) (string, error) {
	var tmpl string
	switch shell {
	case "bash":
		tmpl = bashCompletionTemplate
	case "zsh":
		tmpl = zshCompletionTemplate
	case "fish":
		tmpl = fishCompletionTemplate
	case "powershell":
		tmpl = powershellCompletionTemplate
	case "":
		return "", errors.New("a shell is required (bash, zsh, fish, or powershell)")
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
	return strings.ReplaceAll(tmpl, "PROG", prog), nil
}

const bashCompletionTemplate = `# bash completion for PROG
# Candidates arrive as "name<TAB>description"; bash cannot render descriptions,
# so everything from the first tab is stripped.
_PROG_complete() {
    local args line IFS=$'\n'
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=()
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        COMPREPLY+=("${line%%$'\t'*}")
    done
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# Candidates arrive as "name<TAB>description"; zsh renders the description
# beside the name via _describe (colons in either part are escaped).
_PROG() {
    local -a lines pairs
    local line name desc
    lines=(${(f)"$(PROG __complete ${words[2,$CURRENT]} 2>/dev/null)"})
    for line in $lines; do
        if [[ $line == *$'\t'* ]]; then
            name=${line%%$'\t'*}
            desc=${line#*$'\t'}
            pairs+=("${name//:/\\:}:${desc//:/\\:}")
        else
            pairs+=("${line//:/\\:}")
        fi
    done
    _describe 'PROG' pairs
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
function __PROG_complete
    set -l tokens (commandline -opc) (commandline -ct)
    PROG __complete $tokens[2..-1] 2>/dev/null
end

function __PROG_has_results
    set -g __PROG_results (__PROG_complete)
    test (count $__PROG_results) -gt 0
end

# Offer the binary's candidates when it has any; otherwise fall back to fish's
# file completion (the binary returns nothing for path-valued flags and
# arguments, exactly so the shell takes over). Candidates arrive as
# "name<TAB>description" — fish renders that shape natively.
complete -c PROG -f -n '__PROG_has_results' -a '$__PROG_results'
complete -c PROG -F -n 'not __PROG_has_results'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    if ($wordToComplete -eq '') { $tokens += '' }
    PROG __complete @tokens 2>$null | ForEach-Object {
        # Candidates arrive as "name<TAB>description"; the description becomes
        # the CompletionResult tooltip.
        $parts = $_ -split "` + "`" + `t", 2
        $text = $parts[0]
        $tip = if ($parts.Count -gt 1 -and $parts[1]) { $parts[1] } else { $text }
        [System.Management.Automation.CompletionResult]::new($text, $text, 'ParameterValue', $tip)
    }
}
`
