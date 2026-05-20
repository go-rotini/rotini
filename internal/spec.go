package internal

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"go/format"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	fileNameSpec     = ".rotini.spec.yaml"
	fileNameSpecJSON = ".rotini.spec.json"
	fileNameConf     = ".rotini.conf.yaml"
	fileNameConfJSON = ".rotini.conf.json"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	rotiniTmpl         string
	cmdTmpl            string
	handlersTmpl       string
	rotiniSpecJsonTmpl string
	rotiniSpecYamlTmpl string
	rotiniConfJsonTmpl string
	rotiniConfYamlTmpl string
	mainTmpl           string
)

func init() {
	rotiniTmpl = mustReadTemplate("rotini.go.tmpl")
	cmdTmpl = mustReadTemplate("cmd.go.tmpl")
	handlersTmpl = mustReadTemplate("handlers.go.tmpl")
	rotiniSpecJsonTmpl = mustReadTemplate("spec.json.tmpl")
	rotiniSpecYamlTmpl = mustReadTemplate("spec.yaml.tmpl")
	rotiniConfJsonTmpl = mustReadTemplate("conf.json.tmpl")
	rotiniConfYamlTmpl = mustReadTemplate("conf.yaml.tmpl")
	mainTmpl = mustReadTemplate("main.go.tmpl")
}

func mustReadTemplate(name string) string {
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		panic("missing embedded template: " + name + ": " + err.Error())
	}
	return string(data)
}

// schema is a JSON schema-inspired type definition.
// The Required field is dual-use: for input schemas it holds a bool ("is this input required");
// for object schemas it holds a []string (required property names per JSON schema).
// Use requiredBool() and requiredFields() to read the appropriate form.
type schema struct {
	Ref         string             `json:"$ref,omitempty"`
	Type        string             `json:"type,omitempty"`
	Nullable    bool               `json:"nullable,omitempty"`
	Description string             `json:"description,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Pattern     string             `json:"pattern,omitempty"`
	Minimum     *float64           `json:"minimum,omitempty"`
	Maximum     *float64           `json:"maximum,omitempty"`
	MinLength   *int               `json:"minLength,omitempty"`
	MaxLength   *int               `json:"maxLength,omitempty"`
	MinItems    *int               `json:"minItems,omitempty"`
	MaxItems    *int               `json:"maxItems,omitempty"`
	Required    json.RawMessage    `json:"required,omitempty"`
	Properties  map[string]*schema `json:"properties,omitempty"`
	Items       *schema            `json:"items,omitempty"`

	// Input-level metadata fields (used when schema is attached to a parameter or stdinSpec).
	Default  json.RawMessage `json:"default,omitempty"`
	Variable string          `json:"variable,omitempty"` // env var name (environment inputs)
	File     string          `json:"file,omitempty"`     // config file name (config_values inputs)
	Key      string          `json:"key,omitempty"`      // config file key (config_values inputs)
}

// requiredBool returns the Required field as a bool.
// Returns true only when Required is the JSON value `true`.
func (s *schema) requiredBool() bool {
	if s == nil || len(s.Required) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(s.Required, &b); err != nil {
		return false
	}
	return b
}

// requiredFields returns the Required field as a []string.
// Returns nil when Required is not a JSON array.
func (s *schema) requiredFields() []string {
	if s == nil || len(s.Required) == 0 {
		return nil
	}
	var fields []string
	if err := json.Unmarshal(s.Required, &fields); err != nil {
		return nil
	}
	return fields
}

// parameter is the unified input definition used across flags, arguments, env vars, and config inputs.
// Metadata fields (required, default, etc.) live inside the schema object.
type parameter struct {
	Name        string   `json:"name"`
	Schema      *schema  `json:"schema,omitempty"`
	Identifiers []string `json:"identifiers,omitempty"` // flag-specific: CLI identifiers (e.g., "--force", "-f")
}

// inputs holds the typed sub-arrays of command inputs.
type inputs struct {
	Flags     []parameter `json:"flags,omitempty"`
	Arguments []parameter `json:"arguments,omitempty"`
	Files     []parameter `json:"files,omitempty"`
	Variables []parameter `json:"variables,omitempty"`
	Stdin     *stdinSpec  `json:"stdin,omitempty"`
}

// stdinSpec declares the expected stdin shape for a command's inputs.
// Format is inferred from schema: string type → "text", structured → "json".
// Whether stdin is required is declared via schema.required (bool).
type stdinSpec struct {
	Schema *schema `json:"schema,omitempty"` // expected shape; properties become struct fields
}

// eventSpec declares a single named event a command can emit.
type eventSpec struct {
	Name   string  `json:"name"`
	Schema *schema `json:"schema,omitempty"`
}

// definition is the root program definition: the root command plus program-level declarations.
type definition struct {
	Name           string          `json:"name"`
	Aliases        []string        `json:"aliases,omitempty"`
	Timeout        string          `json:"timeout,omitempty"`
	Inputs         *inputs         `json:"inputs,omitempty"`
	Commands       []command       `json:"commands,omitempty"`
	RemoteCommands []remoteCommand `json:"remote_commands,omitempty"`
	Files          []configSpec    `json:"files,omitempty"`
	Events         []eventSpec     `json:"events,omitempty"`
	Metadata       []metadataEntry `json:"metadata,omitempty"`
}

// command defines a node in the CLI command tree.
type command struct {
	Name           string          `json:"name"`
	Aliases        []string        `json:"aliases,omitempty"`
	Timeout        string          `json:"timeout,omitempty"`
	Inputs         *inputs         `json:"inputs,omitempty"`
	Commands       []command       `json:"commands,omitempty"`
	RemoteCommands []remoteCommand `json:"remote_commands,omitempty"`
}

// configSpec defines a config file that the framework should read at startup.
// If a config file does not exist at the declared path, the framework passes nil bytes
// to the user's On<Name>Read handler — the handler decides how to proceed.
// All config files are hot-reloaded automatically when changes are detected.
type configSpec struct {
	Name   string  `json:"name"`
	Path   string  `json:"path"`
	Format string  `json:"format,omitempty"` // "json" (default), "yaml", or "toml"
	Schema *schema `json:"schema,omitempty"` // when set, generates typed config struct and validates required fields
}

// remoteCommand declares a co-located remote binary dispatched as a first-class command.
// The binary must be named <program>-<name> and located in the same directory as the host binary.
type remoteCommand struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Timeout string   `json:"timeout,omitempty"` // host-side timeout for remote binary execution (Go duration string)
}

// metadataEntry declares a single build-time variable injected via go ldflags.
// Rotini generates a package-level var declaration and a typed struct field for each entry.
type metadataEntry struct {
	Var     string `json:"var"`
	Default string `json:"default,omitempty"`
}

// metadataDef is the codegen representation of a metadataEntry.
type metadataDef struct {
	Var     string
	Default string
}

// generateCmdPruneConfig holds pruning configuration for the command handler package.
type generateCmdPruneConfig struct {
	Enabled bool     `json:"enabled,omitempty"`
	Keep    []string `json:"keep,omitempty"`
}

// generateCmdConfig holds code generation configuration for the command handler package.
type generateCmdConfig struct {
	Package string                 `json:"package,omitempty"`
	GenFile string                 `json:"gen_file,omitempty"`
	Prune   generateCmdPruneConfig `json:"prune"`
}

// generateFrameworkConfig holds code generation configuration for the framework package.
type generateFrameworkConfig struct {
	Package           string   `json:"package,omitempty"`
	GenFile           string   `json:"gen_file,omitempty"`
	AdditionalImports []string `json:"additional_imports,omitempty"`
}

// generateConfig groups the cmd and framework generation configuration.
type generateConfig struct {
	Cmd       generateCmdConfig       `json:"cmd,omitempty"`
	Framework generateFrameworkConfig `json:"framework,omitempty"`
}

// configuration holds the code generation configuration.
type configuration struct {
	Generate generateConfig `json:"generate,omitempty"`
}

// applyConfigurationDefaults applies default values to a configuration.
func applyConfigurationDefaults(config *configuration) {
	if config.Generate.Cmd.Package == "" {
		config.Generate.Cmd.Package = "internal/cli/cmd"
	}
	if config.Generate.Cmd.GenFile == "" {
		config.Generate.Cmd.GenFile = "handlers.gen.go"
	}
	if config.Generate.Framework.Package == "" {
		config.Generate.Framework.Package = "internal/cli/cmd"
	}
	if config.Generate.Framework.GenFile == "" {
		config.Generate.Framework.GenFile = "rotini.gen.go"
	}
}

// specFile is the on-disk representation of a .rotini.spec.{yaml,json} file.
// The definition fields are promoted to the top level (no "definition" wrapper).
type specFile struct {
	Schema  string             `json:"$schema,omitempty"`
	Schemas map[string]*schema `json:"schemas,omitempty"`
	definition
}

// confFile is the on-disk representation of a .rotini.conf.{yaml,json} file.
// The configuration fields are promoted to the top level (no "configuration" wrapper).
type confFile struct {
	Schema string `json:"$schema,omitempty"`
	configuration
}

// spec is the internal aggregation of the definition (from the spec file) and
// the code generation configuration (from the conf file).
type spec struct {
	Schema        string
	Definition    definition
	Schemas       map[string]*schema
	Configuration configuration
}

// newSpec loads a spec from a definition file and a configuration file,
// validates both, and returns the combined internal representation.
// If confPath is empty, the default conf file is discovered automatically.
func newSpec(specPath, confPath string) (*spec, error) {
	if specPath == "" {
		specPath = fileNameSpec
	}
	if confPath == "" {
		confPath = resolveConfPath("")
	}

	sf, err := loadSpecFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load spec file: %w", err)
	}

	cf, err := loadConfFile(confPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load conf file: %w", err)
	}

	s := &spec{
		Schema:        sf.Schema,
		Definition:    sf.definition,
		Schemas:       sf.Schemas,
		Configuration: cf.configuration,
	}

	if err := resolveRefs(s); err != nil {
		return nil, fmt.Errorf("failed to resolve $refs: %w", err)
	}

	if err := validateSchemaVersion(s.Schema); err != nil {
		return nil, err
	}

	applyConfigurationDefaults(&s.Configuration)

	if err := validateConfigurationStructure(&s.Configuration); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return s, nil
}

// loadSpecFile loads and decodes a spec (definition) file.
func loadSpecFile(path string) (*specFile, error) {
	if err := validateSpecFileKeys(path); err != nil {
		return nil, fmt.Errorf("invalid spec file structure: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	isYAML := ext == ".yaml" || ext == ".yml"

	var sf specFile
	if isYAML {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read spec file: %w", err)
		}
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse spec YAML: %w", err)
		}
		jsonBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert spec YAML to JSON: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&sf); err != nil {
			return nil, fmt.Errorf("failed to decode spec: %w", err)
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("failed to open spec file: %w", err)
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&sf); err != nil {
			return nil, fmt.Errorf("failed to parse spec JSON: %w", err)
		}
	}
	return &sf, nil
}

// loadConfFile loads and decodes a configuration file.
func loadConfFile(path string) (*confFile, error) {
	if err := validateConfFileKeys(path); err != nil {
		return nil, fmt.Errorf("invalid conf file structure: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	isYAML := ext == ".yaml" || ext == ".yml"

	var cf confFile
	if isYAML {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read conf file: %w", err)
		}
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse conf YAML: %w", err)
		}
		jsonBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert conf YAML to JSON: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cf); err != nil {
			return nil, fmt.Errorf("failed to decode conf: %w", err)
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("failed to open conf file: %w", err)
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cf); err != nil {
			return nil, fmt.Errorf("failed to parse conf JSON: %w", err)
		}
	}
	return &cf, nil
}

// resolveRefs resolves all $ref fields in the spec tree by inlining the referenced
// component definitions. It handles schema.$ref (→ schemas) and
// response.$ref (→ schemas). Cycle detection is performed per resolution
// chain; a fresh seen map is used for each top-level resolution call.
func resolveRefs(s *spec) error {
	schemas := s.Schemas
	if len(schemas) == 0 {
		return nil
	}

	// Resolve component schemas themselves first (they may reference each other).
	for name, sc := range schemas {
		if sc == nil {
			continue
		}
		if err := resolveSchema(sc, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("schemas/%s: %w", name, err)
		}
	}

	// Resolve root-level inputs.
	if err := resolveInputSchemas(s.Definition.Inputs, schemas, "definition.inputs"); err != nil {
		return err
	}

	// Resolve root-level events.
	for i := range s.Definition.Events {
		if s.Definition.Events[i].Schema != nil {
			if err := resolveSchema(s.Definition.Events[i].Schema, schemas, map[string]bool{}); err != nil {
				return fmt.Errorf("definition.events[%d].schema: %w", i, err)
			}
		}
	}

	// Resolve commands recursively.
	if err := resolveCommands(s.Definition.Commands, schemas); err != nil {
		return err
	}

	return nil
}

// resolveInputSchemas resolves $ref fields in all input kinds of an inputs block.
func resolveInputSchemas(inputs *inputs, schemas map[string]*schema, context string) error {
	if inputs == nil {
		return nil
	}
	for i := range inputs.Flags {
		if err := resolveSchema(inputs.Flags[i].Schema, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("%s.flags[%d].schema: %w", context, i, err)
		}
	}
	for i := range inputs.Arguments {
		if err := resolveSchema(inputs.Arguments[i].Schema, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("%s.arguments[%d].schema: %w", context, i, err)
		}
	}
	for i := range inputs.Files {
		if err := resolveSchema(inputs.Files[i].Schema, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("%s.files[%d].schema: %w", context, i, err)
		}
	}
	for i := range inputs.Variables {
		if err := resolveSchema(inputs.Variables[i].Schema, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("%s.variables[%d].schema: %w", context, i, err)
		}
	}
	if inputs.Stdin != nil {
		if err := resolveSchema(inputs.Stdin.Schema, schemas, map[string]bool{}); err != nil {
			return fmt.Errorf("%s.stdin.schema: %w", context, err)
		}
	}
	return nil
}

// resolveCommands resolves $refs inside a slice of Commands, recursing into subcommands.
func resolveCommands(commands []command, schemas map[string]*schema) error {
	for ci := range commands {
		cmd := &commands[ci]

		// Resolve input schemas.
		if err := resolveInputSchemas(cmd.Inputs, schemas, fmt.Sprintf("command %q inputs", cmd.Name)); err != nil {
			return err
		}

		// Recurse into subcommands.
		if err := resolveCommands(cmd.Commands, schemas); err != nil {
			return err
		}
	}
	return nil
}

// resolveSchema resolves a schema's $ref field and recurses into nested schemas.
// seen tracks the chain for cycle detection.
func resolveSchema(s *schema, schemas map[string]*schema, seen map[string]bool) error {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		if seen[s.Ref] {
			return fmt.Errorf("circular $ref detected: %s", s.Ref)
		}
		seen[s.Ref] = true
		resolved, ok := lookupSchemaRef(s.Ref, schemas)
		if !ok {
			return fmt.Errorf("$ref %q not found", s.Ref)
		}
		// Deep-copy (shallow struct copy) resolved schema into s, then clear Ref.
		*s = *resolved
		s.Ref = ""
		// Recurse: the newly inlined schema may itself have $refs.
		return resolveSchema(s, schemas, seen)
	}

	// Recurse into nested schemas.
	for _, prop := range s.Properties {
		if err := resolveSchema(prop, schemas, seen); err != nil {
			return err
		}
	}
	if err := resolveSchema(s.Items, schemas, seen); err != nil {
		return err
	}
	return nil
}

// lookupSchemaRef parses a ref string of the form "#/schemas/Name"
// and returns the matching schema from the schemas map.
func lookupSchemaRef(ref string, schemas map[string]*schema) (*schema, bool) {
	const prefix = "#/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, false
	}
	name := strings.TrimPrefix(ref, prefix)
	if schemas == nil {
		return nil, false
	}
	s, ok := schemas[name]
	return s, ok
}

// convertDefinition converts a spec into the codegen pipeline's working model.
func convertDefinition(s *spec) convertedDefinition {
	def := s.Definition
	commands := convertCommands(def.Commands, def.Name, "")
	rootFlags, _ := convertInputs(def.Inputs)
	var rootStdin *stdinDefSpec
	if def.Inputs != nil {
		rootStdin = convertStdinSpec(def.Inputs.Stdin)
	}
	configs := convertConfigs(def.Files)
	remoteCommands := convertRemoteCommands(def.Name, def.RemoteCommands)
	allEvents := convertEvents(def.Events, "")
	allCustomTypes := collectCustomTypes(rootFlags, commands)
	return convertedDefinition{
		Commands:           commands,
		RootFlags:          rootFlags,
		RootStdin:          rootStdin,
		ConfigurationFiles: configs,
		RemoteCommands:     remoteCommands,
		AllEvents:          allEvents,
		Timeout:            parseDurationSpec(def.Timeout),
		AllCustomTypes:     allCustomTypes,
	}
}

// convertRemoteCommands converts spec remoteCommand types to remoteCommandDef types.
// defName is the root binary name (used to build the co-located binary name).
func convertRemoteCommands(defName string, rcs []remoteCommand) []remoteCommandDef {
	if len(rcs) == 0 {
		return nil
	}
	result := make([]remoteCommandDef, len(rcs))
	for i, rc := range rcs {
		result[i] = remoteCommandDef{
			Name:       rc.Name,
			PascalName: toPascalCase(rc.Name),
			BinaryName: defName + "-" + rc.Name,
			Aliases:    rc.Aliases,
			Timeout:    parseDurationSpec(rc.Timeout),
		}
	}
	return result
}

func convertCommands(cmds []command, defName, parentPath string) []commandDef {
	result := make([]commandDef, len(cmds))
	for i, cmd := range cmds {
		var cmdPath string
		if parentPath == "" {
			cmdPath = cmd.Name
		} else {
			cmdPath = parentPath + "_" + cmd.Name
		}
		flags, args := convertInputs(cmd.Inputs)
		var stdinSpec *stdinDefSpec
		if cmd.Inputs != nil && cmd.Inputs.Stdin != nil {
			stdinSpec = convertStdinSpec(cmd.Inputs.Stdin)
		}
		result[i] = commandDef{
			Name:           cmd.Name,
			Aliases:        cmd.Aliases,
			Timeout:        parseDurationSpec(cmd.Timeout),
			Commands:       convertCommands(cmd.Commands, defName, cmdPath),
			RemoteCommands: convertRemoteCommands(defName, cmd.RemoteCommands),
			Flags:          flags,
			Arguments:      args,
			Stdin:          stdinSpec,
		}
	}
	return result
}

// flattenAllRemoteCommandDefs collects all remoteCommandDef entries from the entire command tree.
func flattenAllRemoteCommandDefs(commands []commandDef) []remoteCommandDef {
	var result []remoteCommandDef
	for _, cmd := range commands {
		result = append(result, cmd.RemoteCommands...)
		result = append(result, flattenAllRemoteCommandDefs(cmd.Commands)...)
	}
	return result
}

// anyCommandTreeHasTimeout returns true when any command (recursively) has a non-zero Timeout.
func anyCommandTreeHasTimeout(commands []commandDef) bool {
	for _, cmd := range commands {
		if cmd.Timeout != 0 {
			return true
		}
		if anyCommandTreeHasTimeout(cmd.Commands) {
			return true
		}
	}
	return false
}

// anyRemoteCommandHasTimeout returns true when any remote command has a non-zero Timeout.
func anyRemoteCommandHasTimeout(rcs []remoteCommandDef) bool {
	for _, rc := range rcs {
		if rc.Timeout != 0 {
			return true
		}
	}
	return false
}

// needsStringsImport returns true when "strings" is needed in generated code.
func needsStringsImport(opts codegenOptions) bool {
	if opts.RootStdin != nil {
		return true
	}
	flat := flattenCommandDefs(opts.Commands, "")
	for _, cmd := range flat {
		if cmd.Stdin != nil {
			return true
		}
	}
	return false
}

// needsTimeImport returns true when "time" is needed in generated code.
func needsTimeImport(opts codegenOptions) bool {
	if opts.Timeout != 0 {
		return true
	}
	if anyCommandTreeHasTimeout(opts.Commands) {
		return true
	}
	allRC := flattenAllRemoteCommandDefs(opts.Commands)
	if anyRemoteCommandHasTimeout(allRC) || anyRemoteCommandHasTimeout(opts.RemoteCommands) {
		return true
	}
	return false
}

// convertEvents converts eventSpec entries to eventDef for code generation.
// cmdPath is the declaring command's path (empty string for root-level events).
func convertEvents(events []eventSpec, cmdPath string) []eventDef {
	if len(events) == 0 {
		return nil
	}
	result := make([]eventDef, len(events))
	for i, ev := range events {
		def := eventDef{
			Name:        ev.Name,
			PascalName:  toPascalCase(ev.Name),
			CommandPath: cmdPath,
		}
		if ev.Schema != nil {
			if len(ev.Schema.Properties) == 0 && ev.Schema.Type != "" {
				def.PrimitiveType = jsonSchemaTypeToGo(ev.Schema.Type)
			} else {
				def.Fields = schemaPropertiesToFields(ev.Schema.Properties)
			}
		}
		result[i] = def
	}
	return result
}

// convertInputs converts an inputs struct into flagDef and argumentDef slices.
// inputs.Variables entries are converted to env-only flagDefs (no CLI identifiers) and
// appended after the regular flags so they appear in the Args struct.
func convertInputs(inputs *inputs) ([]flagDef, []argumentDef) {
	if inputs == nil {
		return nil, nil
	}
	flags := make([]flagDef, 0, len(inputs.Flags)+len(inputs.Variables))
	for _, p := range inputs.Flags {
		flags = append(flags, paramToFlagDef(p))
	}
	for _, p := range inputs.Variables {
		flags = append(flags, paramToEnvFlagDef(p))
	}
	args := make([]argumentDef, 0, len(inputs.Arguments))
	for _, p := range inputs.Arguments {
		args = append(args, paramToArgDef(p))
	}
	return flags, args
}

// paramToFlagDef converts a flag parameter to a flagDef.
func paramToFlagDef(p parameter) flagDef {
	var typ string
	var required, nullable bool
	var configKey, envVarKey string
	var defaultRaw json.RawMessage
	var enum []string
	var min, max *float64
	var minLen, maxLen, minItems, maxItems *int
	var pattern string
	if p.Schema != nil {
		typ = jsonSchemaTypeToGo(p.Schema.Type)
		required = p.Schema.requiredBool()
		nullable = p.Schema.Nullable
		configKey = p.Schema.Key
		envVarKey = p.Schema.Variable
		defaultRaw = p.Schema.Default
		enum = p.Schema.Enum
		min = p.Schema.Minimum
		max = p.Schema.Maximum
		minLen = p.Schema.MinLength
		maxLen = p.Schema.MaxLength
		minItems = p.Schema.MinItems
		maxItems = p.Schema.MaxItems
		pattern = p.Schema.Pattern
	}
	if typ == "" {
		typ = "string"
	}
	identifiers := p.Identifiers
	if len(identifiers) == 0 {
		identifiers = []string{"--" + strings.ReplaceAll(p.Name, "_", "-")}
	}
	return flagDef{
		Name:        p.Name,
		Identifiers: identifiers,
		Type:        typ,
		Required:    required,
		Default:     defaultStr(defaultRaw),
		Enum:        enum,
		ConfigKey:   configKey,
		EnvVarKey:   envVarKey,
		Min:         min,
		Max:         max,
		MinLength:   minLen,
		MaxLength:   maxLen,
		MinItems:    minItems,
		MaxItems:    maxItems,
		Pattern:     pattern,
		Nullable:    nullable,
	}
}

// paramToEnvFlagDef converts an environment input parameter to a flagDef with no CLI identifiers.
// These flags are sourced exclusively from the declared environment variable.
func paramToEnvFlagDef(p parameter) flagDef {
	fd := paramToFlagDef(p)
	fd.Identifiers = nil // no CLI form
	fd.EnvOnly = true
	if fd.EnvVarKey == "" && p.Schema != nil {
		fd.EnvVarKey = p.Schema.Variable
	}
	return fd
}

// paramToArgDef converts an argument parameter to an argumentDef.
// Variadic is inferred from the schema type: array types ([]string, []int, etc.) are variadic.
func paramToArgDef(p parameter) argumentDef {
	var typ string
	var required, nullable bool
	var configKey, envVarKey string
	var defaultRaw json.RawMessage
	var enum []string
	var pattern string
	if p.Schema != nil {
		typ = jsonSchemaTypeToGo(p.Schema.Type)
		required = p.Schema.requiredBool()
		nullable = p.Schema.Nullable
		configKey = p.Schema.Key
		envVarKey = p.Schema.Variable
		defaultRaw = p.Schema.Default
		enum = p.Schema.Enum
		pattern = p.Schema.Pattern
	}
	if typ == "" {
		typ = "string"
	}
	// Variadic is inferred: array schema type ([]string, etc.) means variadic
	variadic := strings.HasPrefix(typ, "[]")
	return argumentDef{
		Name:      p.Name,
		Type:      typ,
		Required:  required,
		Variadic:  variadic,
		Default:   defaultStr(defaultRaw),
		ConfigKey: configKey,
		EnvVarKey: envVarKey,
		Enum:      enum,
		Pattern:   pattern,
		Nullable:  nullable,
	}
}

// defaultStr converts a json.RawMessage default value to its string representation.
// Strings have their quotes stripped; other types (numbers, booleans) are returned as-is.
func defaultStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// If it's a JSON string, unquote it
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Otherwise return the raw JSON (for numbers, booleans, etc.)
	return string(raw)
}

// schemaPropertiesToFields converts a map of JSON schema properties to a sorted
// slice of responseFieldDef. Properties are sorted by name for deterministic output.
func schemaPropertiesToFields(properties map[string]*schema) []responseFieldDef {
	if len(properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	var fields []responseFieldDef
	for _, name := range names {
		prop := properties[name]
		if prop != nil && prop.Type != "" {
			fields = append(fields, responseFieldDef{
				Name: name,
				Type: jsonSchemaTypeToGo(prop.Type),
			})
		}
	}
	return fields
}

// parseDurationSpec parses a Go duration string from a spec field.
// Returns 0 if the string is empty or cannot be parsed.
func parseDurationSpec(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

// convertStdinSpec converts a stdinSpec to a stdinDefSpec for code generation.
// Format is inferred from schema: string type → "text", structured/nil → "json".
func convertStdinSpec(s *stdinSpec) *stdinDefSpec {
	if s == nil {
		return nil
	}
	// Infer format from schema type
	format := "json"
	var required bool
	if s.Schema != nil {
		if s.Schema.Type == "string" {
			format = "text"
		}
		required = s.Schema.requiredBool()
	}
	var fields []responseFieldDef
	if s.Schema != nil {
		fields = schemaPropertiesToFields(s.Schema.Properties)
	}
	return &stdinDefSpec{Format: format, Required: required, Fields: fields}
}

// convertConfigs converts spec configSpec types to generate configDef types.
func convertConfigs(configs []configSpec) []configDef {
	if len(configs) == 0 {
		return nil
	}
	result := make([]configDef, len(configs))
	for i, c := range configs {
		var schemaFields []responseFieldDef
		var schemaRequired []string
		if c.Schema != nil {
			schemaFields = schemaPropertiesToFields(c.Schema.Properties)
			schemaRequired = c.Schema.requiredFields()
		}
		result[i] = configDef{
			Name:              c.Name,
			Path:              c.Path,
			Format:            c.Format,
			Schema:            schemaFields,
			SchemaRequired:    schemaRequired,
			HotReloadDebounce: 200 * time.Millisecond,
		}
	}
	return result
}

// templateFuncs returns template helper functions for code generation.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"toPascalCase":       toPascalCase,
		"quoteSlice":         quoteSlice,
		"flattenCommandDefs": flattenCommandDefs,
		"sortedKeys":         sortedKeys[commandDef],
		"renderRotiniCommandLiteral": func(cmd commandDef) string {
			return strings.TrimRight(renderCommandMetaLiteral(cmd, "\t\t"), "\n")
		},
		"renderRotiniFlag": func(f flagDef) string { return strings.TrimRight(renderFlagMetaLiteral(f, ""), "\n") },
		"isPrimitiveType":  isPrimitiveType,
		"goType":           goType,
		"isMapType":        isMapType,
		"isSliceType":      isSliceType,
		"hasCheckExpr":     hasCheckExpr,
		"formatImport": func(imp string) string {
			parts := strings.SplitN(imp, " ", 2)
			if len(parts) == 2 {
				return parts[0] + ` "` + parts[1] + `"`
			}
			return `"` + imp + `"`
		},
		"reverseStrings": func(s []string) []string {
			n := len(s)
			out := make([]string, n)
			for i, v := range s {
				out[n-1-i] = v
			}
			return out
		},
		"allPreRunAncestors": func(path string, flat map[string]commandDef) []string {
			parts := strings.Split(path, "-")
			var result []string
			for i := 1; i < len(parts); i++ {
				ancestorPath := strings.Join(parts[:i], "-")
				if _, ok := flat[ancestorPath]; ok {
					result = append(result, ancestorPath)
				}
			}
			return result
		},
		"commandIntermediateAncestorPaths": func(path string) []string {
			parts := strings.Split(path, "-")
			var result []string
			for i := 1; i < len(parts); i++ {
				result = append(result, strings.Join(parts[:i], "-"))
			}
			return result
		},
		"lastPathSegment": func(path string) string {
			parts := strings.Split(path, "-")
			return parts[len(parts)-1]
		},
		"renderFlagSpecLiterals":     renderFlagSpecLiterals,
		"renderArgumentSpecLiterals": renderArgumentSpecLiterals,
		"renderCommandSpecLiterals":  renderCommandSpecLiterals,
		"goStringSlice":              goStringSliceLiteral,
	}
}

// renderFlagSpecLiterals renders a []CommandFlagSpec{...} literal for the given flags.
// Returns "nil" when the slice is empty.
func renderFlagSpecLiterals(flags []flagDef) string {
	if len(flags) == 0 {
		return "nil"
	}
	var sb strings.Builder
	sb.WriteString("[]CommandFlagSpec{\n")
	for _, f := range flags {
		sb.WriteString("\t\t\t\t{")
		fmt.Fprintf(&sb, "Name: %q", f.Name)
		if len(f.Identifiers) > 0 {
			fmt.Fprintf(&sb, ", Identifiers: %s", goStringSliceLiteral(f.Identifiers))
		}
		if f.Type != "" {
			fmt.Fprintf(&sb, ", Type: %q", f.Type)
		}
		if f.Default != "" {
			fmt.Fprintf(&sb, ", Default: %q", f.Default)
		}
		if len(f.Enum) > 0 {
			fmt.Fprintf(&sb, ", Enum: %s", goStringSliceLiteral(f.Enum))
		}
		if f.Required {
			sb.WriteString(", Required: true")
		}
		sb.WriteString("},\n")
	}
	sb.WriteString("\t\t\t}")
	return sb.String()
}

// renderArgumentSpecLiterals renders a []CommandArgumentSpec{...} literal for the given arguments.
// Returns "nil" when the slice is empty.
func renderArgumentSpecLiterals(args []argumentDef) string {
	if len(args) == 0 {
		return "nil"
	}
	var sb strings.Builder
	sb.WriteString("[]CommandArgumentSpec{\n")
	for _, a := range args {
		sb.WriteString("\t\t\t\t{")
		fmt.Fprintf(&sb, "Name: %q", a.Name)
		if a.Type != "" {
			fmt.Fprintf(&sb, ", Type: %q", a.Type)
		}
		if a.Default != "" {
			fmt.Fprintf(&sb, ", Default: %q", a.Default)
		}
		if len(a.Enum) > 0 {
			fmt.Fprintf(&sb, ", Enum: %s", goStringSliceLiteral(a.Enum))
		}
		if a.Required {
			sb.WriteString(", Required: true")
		}
		if a.Variadic {
			sb.WriteString(", Variadic: true")
		}
		sb.WriteString("},\n")
	}
	sb.WriteString("\t\t\t}")
	return sb.String()
}

// renderCommandSpecLiterals renders a []CommandSpec{...} literal for the given subcommands.
// Returns "nil" when the slice is empty.
func renderCommandSpecLiterals(cmds []commandDef) string {
	if len(cmds) == 0 {
		return "nil"
	}
	var sb strings.Builder
	sb.WriteString("[]CommandSpec{\n")
	for _, c := range cmds {
		sb.WriteString(renderCommandSpecEntry(c, "\t\t\t\t"))
	}
	sb.WriteString("\t\t\t}")
	return sb.String()
}

func renderCommandSpecEntry(c commandDef, indent string) string {
	var sb strings.Builder
	sb.WriteString(indent + "{\n")
	fmt.Fprintf(&sb, indent+"\tName: %q,\n", c.Name)
	if len(c.Aliases) > 0 {
		fmt.Fprintf(&sb, indent+"\tAliases: %s,\n", goStringSliceLiteral(c.Aliases))
	}
	if len(c.Flags) > 0 {
		sb.WriteString(indent + "\tFlags: " + renderFlagSpecLiterals(c.Flags) + ",\n")
	}
	if len(c.Arguments) > 0 {
		sb.WriteString(indent + "\tArguments: " + renderArgumentSpecLiterals(c.Arguments) + ",\n")
	}
	if len(c.Commands) > 0 {
		sb.WriteString(indent + "\tCommands: []CommandSpec{\n")
		for _, sub := range c.Commands {
			sb.WriteString(renderCommandSpecEntry(sub, indent+"\t\t"))
		}
		sb.WriteString(indent + "\t},\n")
	}
	sb.WriteString(indent + "},\n")
	return sb.String()
}

// commandTreeFilename derives the filename segment for a command based on its ancestry chain.
// Top-level commands use their name directly; subcommands use underscore-separated parent-child names.
// e.g., "get" → "get", parent="config" child="get" → "config_get"
func commandTreeFilename(parentPath, name string) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "_" + name
}

// flattenCommandDefs returns a sorted map of command tree path → commandDef for all commands.
func flattenCommandDefs(commands []commandDef, parentPath string) map[string]commandDef {
	result := make(map[string]commandDef)
	for _, cmd := range commands {
		path := commandTreeFilename(parentPath, cmd.Name)
		result[path] = cmd
		maps.Copy(result, flattenCommandDefs(cmd.Commands, path))
	}
	return result
}

// sortedKeys returns the sorted keys of a map.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toPascalCase converts a string to PascalCase.
func toPascalCase(s string) string {
	if s == "" {
		return ""
	}

	var result []rune
	capitalize := true

	for _, ch := range s {
		if ch == '-' || ch == '_' || ch == ' ' {
			capitalize = true
			continue
		}

		if capitalize {
			result = append(result, toUpper(ch))
			capitalize = false
		} else {
			result = append(result, ch)
		}
	}

	return string(result)
}

// toUpper converts a rune to uppercase using unicode.
func toUpper(r rune) rune {
	return unicode.ToUpper(r)
}

// implResult tracks impl file operations for generateResult.
type implResult struct {
	FilesCreated        []string
	FilesRemoved        []string
	FilesSkipped        []string
	HandlersRegenerated bool
}

// syncImplFiles synchronizes handler impl files with the current definition.
// It creates missing command stubs, removes orphaned files, always regenerates
// the handlers rollup file (rollupFile within outputDir), and always regenerates
// .help.txt files for every command (including root).
func syncImplFiles(packageName, outputDir, rollupFile, binName string, commands []commandDef, rootFlags []flagDef, configs []configDef, allEvents []eventDef, imports []string, prune generateCmdPruneConfig, merged bool, frameworkImportPath string, skipRollup bool) (*implResult, error) {
	result := &implResult{}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create handler output directory: %w", err)
	}

	if rollupFile == "" {
		rollupFile = "program.gen.go"
	}

	// Determine per-command file naming
	cmdFilePrefix := ""
	if binName != "" {
		cmdFilePrefix = binName + "_"
	}

	// Determine expected command files (using new naming: {binName}-{cmdPath}.go)
	cmdPaths := sortedKeys(flattenCommandDefs(commands, ""))
	expectedCmdFiles := make(map[string]bool)
	for _, path := range cmdPaths {
		expectedCmdFiles[cmdFilePrefix+path+".go"] = true
	}
	// Determine the binName file name ({binName}.go or program.go fallback)
	binFileName := "program.go"
	if binName != "" {
		binFileName = binName + ".go"
	}

	// Write {binName}.go (lifecycle/flag handlers): create if missing.
	binFile := filepath.Join(outputDir, binFileName)
	_, binStatErr := os.Stat(binFile)
	if os.IsNotExist(binStatErr) {
		content, err := renderImplProgramFile(packageName, binName, allEvents, imports, merged, frameworkImportPath)
		if err != nil {
			return nil, fmt.Errorf("failed to render %s: %w", binFileName, err)
		}
		if _, err := writeGeneratedFile(binFile, content); err != nil {
			return nil, err
		}
		result.FilesCreated = append(result.FilesCreated, binFile)
	} else {
		result.FilesSkipped = append(result.FilesSkipped, binFile)
	}

	// Write command stub files: create if missing.
	cmdDefs := flattenCommandDefs(commands, "")
	for _, path := range sortedKeys(cmdDefs) {
		cmd := cmdDefs[path]
		filename := cmdFilePrefix + path + ".go"
		filePath := filepath.Join(outputDir, filename)

		_, statErr := os.Stat(filePath)
		if os.IsNotExist(statErr) {
			content, err := renderImplCommandFile(packageName, binName, cmd, path, allEvents, imports, merged, frameworkImportPath)
			if err != nil {
				return nil, fmt.Errorf("failed to render %s: %w", filename, err)
			}
			if _, err := writeGeneratedFile(filePath, content); err != nil {
				return nil, err
			}
			result.FilesCreated = append(result.FilesCreated, filePath)
		} else {
			result.FilesSkipped = append(result.FilesSkipped, filePath)
		}
	}

	// Remove orphaned impl command files.
	// Legacy artifact cleanup (.rotinigen.go, .tmpl, .help.txt) is always performed.
	// User stub cleanup (non-expected .go files) is controlled by prune.
	entries, err := os.ReadDir(outputDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			// Always clean up legacy .rotinigen.go files
			if !entry.IsDir() && strings.HasSuffix(name, ".rotinigen.go") {
				orphanPath := filepath.Join(outputDir, name)
				if err := os.Remove(orphanPath); err == nil {
					result.FilesRemoved = append(result.FilesRemoved, orphanPath)
				}
				continue
			}
			// Always remove orphaned .tmpl files (no longer generated by rotini)
			if !entry.IsDir() && strings.HasSuffix(name, ".tmpl") {
				orphanPath := filepath.Join(outputDir, name)
				if err := os.Remove(orphanPath); err == nil {
					result.FilesRemoved = append(result.FilesRemoved, orphanPath)
				}
				continue
			}
			// Always remove .help.txt files (help is now inline in stub files)
			if !entry.IsDir() && strings.HasSuffix(name, ".help.txt") {
				orphanPath := filepath.Join(outputDir, name)
				if err := os.Remove(orphanPath); err == nil {
					result.FilesRemoved = append(result.FilesRemoved, orphanPath)
				}
				continue
			}
			// Remove user stub files that no longer correspond to a command — only when prune.enabled=true.
			if prune.Enabled && !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".gen.go") {
				if name == binFileName || expectedCmdFiles[name] {
					continue
				}
				kept := false
				for _, k := range prune.Keep {
					if name == k {
						kept = true
						break
					}
				}
				if !kept {
					orphanPath := filepath.Join(outputDir, name)
					if err := os.Remove(orphanPath); err == nil {
						result.FilesRemoved = append(result.FilesRemoved, orphanPath)
					}
				}
			}
		}
	}

	// In Case 3 (same package + same gen_file) the Handlers rollup is embedded
	// in the framework template output; skip generating a separate rollup file.
	if !skipRollup {
		handlersFile := filepath.Join(outputDir, rollupFile)
		content, err := renderImplHandlersFile(packageName, commands, cmdFilePrefix, binName, merged, frameworkImportPath)
		if err != nil {
			return nil, fmt.Errorf("failed to render %s: %w", rollupFile, err)
		}
		status, err := writeGeneratedFile(handlersFile, content)
		if err != nil {
			return nil, err
		}
		switch status {
		case wroteModified:
			result.HandlersRegenerated = true
			result.FilesCreated = append(result.FilesCreated, handlersFile)
		case wroteCreated:
			result.FilesCreated = append(result.FilesCreated, handlersFile)
		case wroteUnchanged:
			result.FilesSkipped = append(result.FilesSkipped, handlersFile)
		}
	}

	sort.Strings(result.FilesCreated)
	sort.Strings(result.FilesRemoved)
	sort.Strings(result.FilesSkipped)

	return result, nil
}

// isTypedCommandOutput reports whether the command produces a named result struct
// rather than a plain string. Mirrors the hasTypedCommandOutput template function.
// frameworkAlias returns the import alias for the framework package derived from its import path.
// Returns an empty string when merged (no import needed).
func frameworkAlias(importPath string) string {
	if importPath == "" {
		return ""
	}
	return filepath.Base(importPath)
}

// frameworkPrefix returns the qualifier prefix (e.g. "rotini.") for type references in
// handler stubs when the framework package is separate. Returns "" in merged mode.
func frameworkPrefix(importPath string) string {
	alias := frameworkAlias(importPath)
	if alias == "" {
		return ""
	}
	return alias + "."
}

// renderImplProgramFile renders {binName}.go with {BinName}Handlers struct.
func renderImplProgramFile(packageName, binName string, allEvents []eventDef, imports []string, merged bool, fwImportPath string) ([]byte, error) {
	data := map[string]any{
		"CommandsPackageName": packageName,
		"RootCommandName":     binName,
		"CommandName":         "",
		"CommandArgsType":     toPascalCase(binName) + "Args",
		"Imports":             imports,
		"HasEvents":           len(allEvents) > 0,
		"FrameworkImport":     fwImportPath,
		"FrameworkAlias":      frameworkAlias(fwImportPath),
		"FrameworkPrefix":     frameworkPrefix(fwImportPath),
	}
	return renderTemplate("program-impl", cmdTmpl, data)
}

// renderImplCommandFile renders a single per-command .go file.
func renderImplCommandFile(packageName, binName string, cmd commandDef, cmdPath string, allEvents []eventDef, imports []string, merged bool, fwImportPath string) ([]byte, error) {
	pascalName := toPascalCase(cmdPath)
	data := map[string]any{
		"CommandsPackageName": packageName,
		"RootCommandName":     binName,
		"command":             cmd,
		"CommandPath":         cmdPath,
		"CommandName":         pascalName,
		"CommandArgsType":     toPascalCase(binName) + pascalName + "Args",
		"Imports":             imports,
		"HasEvents":           len(allEvents) > 0,
		"FrameworkImport":     fwImportPath,
		"FrameworkAlias":      frameworkAlias(fwImportPath),
		"FrameworkPrefix":     frameworkPrefix(fwImportPath),
	}
	return renderTemplate("command-impl-"+cmdPath, cmdTmpl, data)
}

// renderImplHandlersFile renders the handlers rollup file (Handlers struct + Run method).
func renderImplHandlersFile(packageName string, commands []commandDef, cmdFilePrefix, binName string, merged bool, fwImportPath string) ([]byte, error) {
	var cmdNames []string
	for _, path := range sortedKeys(flattenCommandDefs(commands, "")) {
		cmdNames = append(cmdNames, toPascalCase(path))
	}

	aggHandlerType := "Handlers"
	if merged {
		aggHandlerType = "program"
	}

	data := map[string]any{
		"CommandsPackageName": packageName,
		"CommandNames":        cmdNames,
		"CmdFilePrefix":       cmdFilePrefix,
		"RootCommandName":     binName,
		"AggHandlerType":      aggHandlerType,
		"FrameworkImport":     fwImportPath,
		"FrameworkAlias":      frameworkAlias(fwImportPath),
		"FrameworkPrefix":     frameworkPrefix(fwImportPath),
	}
	return renderTemplate("handlers-impl", handlersTmpl, data)
}

// jsonSchemaTypeToGo maps JSON schema standard type names to Go type names.
// Known JSON schema type names are translated; all other values (including
// already-valid Go type names and custom types like "url.URL") pass through unchanged.
func jsonSchemaTypeToGo(t string) string {
	switch t {
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
	case "time", "datetime":
		return "time.Time"
	case "date":
		return "time.Time"
	default:
		return t
	}
}

// aliasTypeToGo maps built-in type names to Go type expressions.
// Returns the input unchanged for standard Go types (string, bool, int, etc.).
func aliasTypeToGo(typeName string) string {
	switch typeName {
	case "duration":
		return "time.Duration"
	default:
		return typeName
	}
}

// goType returns the Go type expression for a type name (alias-resolved).
func goType(typeName string) string {
	return aliasTypeToGo(typeName)
}

// isSliceType reports whether typeName resolves to a Go slice type.
func isSliceType(typeName string) bool {
	return strings.HasPrefix(aliasTypeToGo(typeName), "[]")
}

// isMapType reports whether typeName is a Go map type.
func isMapType(typeName string) bool {
	return strings.HasPrefix(typeName, "map[")
}

// hasCheckExpr returns the boolean expression body for a Has*() helper.
// fieldName must be the struct field name (PascalCase).
// nullable indicates whether the field is a pointer type (*T), in which case nil-check is used.
func hasCheckExpr(fieldName, typeName string, nullable bool) string {
	if nullable {
		return "f." + fieldName + " != nil"
	}
	goTyp := aliasTypeToGo(typeName)
	if strings.HasPrefix(goTyp, "[]") || strings.HasPrefix(goTyp, "map[") {
		return "len(f." + fieldName + ") > 0"
	}
	switch goTyp {
	case "bool":
		return "f." + fieldName
	case "time.Time":
		return "!f." + fieldName + ".IsZero()"
	case "string":
		return `f.` + fieldName + ` != ""`
	case "int", "int32", "int64", "uint", "uint32", "uint64", "float64", "time.Duration":
		return "f." + fieldName + " != 0"
	case "net.IP":
		return "f." + fieldName + " != nil"
	case "*url.URL", "*regexp.Regexp":
		return "f." + fieldName + " != nil"
	default:
		// Custom types: use reflect.DeepEqual against the zero value so the generated
		// code compiles for struct types (comparing a struct to 0 is invalid in Go).
		return "!reflect.DeepEqual(f." + fieldName + ", (" + goTyp + "){})"
	}
}

// collectCustomTypes walks all root flags and every command (recursively) to collect
// the deduplicated set of non-primitive type names. These drive the TextUnmarshaler
// cases generated inside coerceValue in the template.
func collectCustomTypes(rootFlags []flagDef, commands []commandDef) []string {
	seen := map[string]bool{}
	var add func(typ string)
	add = func(typ string) {
		if typ == "" || isPrimitiveType(typ) || seen[typ] {
			return
		}
		seen[typ] = true
	}
	var walkFlags func(flags []flagDef)
	walkFlags = func(flags []flagDef) {
		for _, f := range flags {
			add(f.resolvedType())
		}
	}
	var walkCommands func(cmds []commandDef)
	walkCommands = func(cmds []commandDef) {
		for _, cmd := range cmds {
			walkFlags(cmd.Flags)
			for _, a := range cmd.Arguments {
				add(a.resolvedType())
			}
			walkCommands(cmd.Commands)
		}
	}
	walkFlags(rootFlags)
	walkCommands(commands)

	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// isPrimitiveType reports whether typeName is a type that coerceValue handles natively.
// Returns false for any custom / third-party types that require TextUnmarshaler.
func isPrimitiveType(typeName string) bool {
	switch typeName {
	case "bool", "string",
		"int", "int32", "int64", "uint", "uint32", "uint64",
		"float", "float64",
		"duration", "time.Duration", "time.Time", "time", "datetime", "date",
		"net.IP", "*url.URL", "*regexp.Regexp",
		"[]string", "[]int", "[]float64", "[]float", "[]bool",
		"map[string]string", "map[string]int", "map[string]bool", "map[string]float64":
		return true
	}
	return false
}

// goStringSliceLiteral renders a Go []string{...} literal for the given strings.
// Returns "nil" when the slice is empty.
func goStringSliceLiteral(ss []string) string {
	if len(ss) == 0 {
		return "nil"
	}
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return `[]string{` + strings.Join(quoted, ", ") + `}`
}

// writeStatus describes what writeGeneratedFile did.
type writeStatus int

const (
	wroteCreated   writeStatus = iota // file did not exist, created
	wroteModified                     // file existed, content changed
	wroteUnchanged                    // file existed, content identical — no write
)

// writeGeneratedFile writes formatted code to the specified path.
// Returns a writeStatus indicating whether the file was created, modified, or unchanged.
// Skips the write when the existing content is identical, avoiding unnecessary
// go build cache invalidation.
func writeGeneratedFile(path string, content []byte) (writeStatus, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return wroteUnchanged, fmt.Errorf("failed to create output directory: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, content) {
			return wroteUnchanged, nil
		}
		// File exists but content differs.
		if err := os.WriteFile(path, content, 0644); err != nil {
			return wroteUnchanged, fmt.Errorf("failed to write file %s: %w", path, err)
		}
		return wroteModified, nil
	}

	// File does not exist — create it.
	if err := os.WriteFile(path, content, 0644); err != nil {
		return wroteUnchanged, fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return wroteCreated, nil
}

// renderTemplate renders a template with the given data and returns formatted Go source.
func renderTemplate(name, tmplStr string, data map[string]any) ([]byte, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs()).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %q: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template %q: %w", name, err)
	}

	// Run gofmt for canonical formatting
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to format generated code (gofmt): %w", err)
	}

	return formatted, nil
}

// genResult tracks gen file operations for generateResult.
type genResult struct {
	FilesCreated   []string
	FilesModified  []string
	FilesRemoved   []string
	FilesUnchanged []string
}

// generateProgramFile generates the framework runtime code into a single rotini.gen.go file.
func generateProgramFile(opts codegenOptions) (*genResult, error) {
	result := &genResult{}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	outputFileName := opts.OutputFile
	if outputFileName == "" {
		outputFileName = "rotini.gen.go"
	}

	// In merged mode the framework types live in the same package as handler
	// stubs, so the aggregate interface must be unexported ("program") to avoid
	// colliding with the user's Handlers struct.  In separate mode the framework
	// package is distinct and the interface is exported ("Handlers").
	aggHandlerType := "Handlers"
	if opts.Merged {
		aggHandlerType = "program"
	}

	// Build FlattenedCommands for the per-command range loop in the combined template.
	cmdDefs := flattenCommandDefs(opts.Commands, "")
	var flatCmds []flattenedCommandEntry
	for _, path := range sortedKeys(cmdDefs) {
		flatCmds = append(flatCmds, flattenedCommandEntry{
			CommandPath: path,
			Command:     cmdDefs[path],
		})
	}

	// Shared template data available to all sections.
	data := map[string]any{
		"Package":            opts.PackageName,
		"Commands":           opts.Commands,
		"RootFlags":          opts.RootFlags,
		"RootStdin":          opts.RootStdin,
		"ConfigurationFiles": opts.ConfigurationFiles,
		"RemoteCommands":     opts.RemoteCommands,
		"AllRemoteCommands":  append(opts.RemoteCommands, flattenAllRemoteCommandDefs(opts.Commands)...),
		"AllEvents":          opts.AllEvents,
		"Name":               opts.Bin.Name,
		"BinDisplayName":     opts.Bin.DisplayName,
		"AggHandlerType":     aggHandlerType,
		"SameFile":           opts.SameFile,
		"Imports":            opts.Imports,
		"FlattenedCommands":  flatCmds,
		"Metadata":           opts.Metadata,
		"AllCustomTypes":     opts.AllCustomTypes,
		"NeedsStringsImport": needsStringsImport(opts),
		"NeedsTimeImport":    needsTimeImport(opts),
	}

	rendered, err := renderTemplate("rotini.gen.go", rotiniTmpl, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render rotini.gen.go: %w", err)
	}

	formatted, err := format.Source(rendered)
	if err != nil {
		// renderTemplate already formats, but re-formatting here catches any edge cases
		formatted = rendered
	}

	outputPath := filepath.Join(opts.OutputDir, outputFileName)
	status, err := writeGeneratedFile(outputPath, formatted)
	if err != nil {
		return nil, err
	}
	switch status {
	case wroteCreated:
		result.FilesCreated = append(result.FilesCreated, outputPath)
	case wroteModified:
		result.FilesModified = append(result.FilesModified, outputPath)
	case wroteUnchanged:
		result.FilesUnchanged = append(result.FilesUnchanged, outputPath)
	}

	// Remove stale per-file .gen.go artifacts from before the template consolidation.
	staleNames := map[string]bool{
		"lifecycle.gen.go":        true,
		"metadata.gen.go":         true,
		"parser.gen.go":           true,
		"router.gen.go":           true,
		"help.gen.go":             true,
		"dispatch.gen.go":         true,
		"defaults.gen.go":         true,
		"repl.gen.go":             true,
		"remote_commands.gen.go":  true,
		"lifecycle_types.gen.go":  true,
		"metadata_types.gen.go":   true,
		"output.gen.go":           true,
		"utils.gen.go":            true,
		"validation.gen.go":       true,
		"tickers.gen.go":          true,
		"signals.gen.go":          true,
		"platform.gen.go":         true,
		"parser_runtime.gen.go":   true,
		"router_runtime.gen.go":   true,
		"help_runtime.gen.go":     true,
		"defaults_runtime.gen.go": true,
	}
	entries, err := os.ReadDir(opts.OutputDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && staleNames[entry.Name()] {
				path := filepath.Join(opts.OutputDir, entry.Name())
				if err := os.Remove(path); err == nil {
					result.FilesRemoved = append(result.FilesRemoved, path)
				}
			}
		}
	}

	sort.Strings(result.FilesCreated)
	sort.Strings(result.FilesModified)
	sort.Strings(result.FilesRemoved)
	sort.Strings(result.FilesUnchanged)

	return result, nil
}

// findModuleRoot walks up from CWD to find the directory containing go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("go.mod not found in any parent directory")
}

// getModuleName finds and reads the go.mod file, walking up from CWD, and extracts the module name.
func getModuleName() (string, error) {
	root, err := findModuleRoot()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod: %w", err)
	}

	for line := range bytes.SplitSeq(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("module ")) {
			moduleName := string(bytes.TrimSpace(trimmed[7:]))
			return moduleName, nil
		}
	}

	return "", fmt.Errorf("module name not found in go.mod")
}

// calculateImportPath calculates the full import path from module name and directory path.
func calculateImportPath(moduleName, dirPath string) string {
	dirPath = filepath.ToSlash(dirPath)
	return moduleName + "/" + dirPath
}

// Codegen types — the internal representation used by the code generation pipeline.
//
// These types mirror the spec types in internal/spec (command, Flag, Argument)
// but without JSON tags. The separation is intentional:
//   - spec types (internal/spec) handle JSON deserialization and own the user-facing schema.
//   - Codegen types (this file) are the pipeline's working model, free to evolve independently.
//   - convert*() functions in convert.go bridge spec → codegen.
//
// This avoids coupling code generation logic to JSON serialization concerns
// and allows either side to add fields without affecting the other.

// stdinDefSpec carries compiled stdin spec for the code generation pipeline.
type stdinDefSpec struct {
	Format   string             // "json" or "text" — inferred from schema type
	Required bool               // if true, error when stdin is empty
	Fields   []responseFieldDef // sorted fields from schema.properties
}

// remoteCommandDef represents a remote command definition for code generation.
type remoteCommandDef struct {
	Name       string
	PascalName string // toPascalCase(Name)
	BinaryName string // "<defName>-<Name>", e.g. "rotini-remote-one"
	Aliases    []string
	Timeout    time.Duration // host-side timeout for remote binary execution
}

// eventDef is the codegen representation of a declared event.
type eventDef struct {
	Name          string             // original name string (e.g. "user_created")
	PascalName    string             // toPascalCase(Name) (e.g. "UserCreated")
	Fields        []responseFieldDef // struct fields from schema.properties (empty for scalar/no-schema)
	PrimitiveType string             // Go scalar type when schema is a primitive (e.g. "string", "int")
	CommandPath   string             // path of the declaring command ("" for root)
}

// commandDef represents a command definition for code generation.
type commandDef struct {
	Name           string
	Aliases        []string
	Timeout        time.Duration // auto-cancel OnRun after this duration
	Commands       []commandDef
	RemoteCommands []remoteCommandDef // remote sub-commands declared under this command
	Flags          []flagDef
	Arguments      []argumentDef
	Stdin          *stdinDefSpec // typed stdin spec (when stdin is declared in inputs)
}

// responseFieldDef is a single field in a generated response struct.
type responseFieldDef struct {
	Name string // Go field name (will be PascalCased in generated code)
	Type string // Go type string (e.g. "string", "[]string", "int")
}

// flagDef represents a flag definition for code generation.
type flagDef struct {
	Name        string
	Identifiers []string // CLI identifiers (e.g., "--force", "-f")
	Type        string   // any valid Go type string ("bool", "string", "time.Duration", etc.)
	Required    bool
	Default     string   // default value (applied when flag not provided)
	Enum        []string // allowed values; auto-validated at parse time
	ConfigKey   string   // config file dot-path key (checked when flag not provided via CLI or env)
	EnvVarKey   string   // environment variable name (checked when flag not provided via CLI)
	Min         *float64 // minimum value (int/float types); auto-validated at parse time
	Max         *float64 // maximum value (int/float types); auto-validated at parse time
	MinLength   *int     // minimum string length (string type); auto-validated at parse time
	MaxLength   *int     // maximum string length (string type); auto-validated at parse time
	MinItems    *int     // minimum item count (slice types); auto-validated at parse time
	MaxItems    *int     // maximum item count (slice types); auto-validated at parse time
	Pattern     string   // regex pattern (string type); auto-validated at parse time
	Nullable    bool     // when true, generates *T field type instead of T
	EnvOnly     bool     // when true, flag has no CLI identifiers (env/config source only)
}

// resolvedType returns the flag's type, defaulting to "string" if unset.
func (f flagDef) resolvedType() string {
	if f.Type == "" {
		return "string"
	}
	return f.Type
}

// ResolvedType is the exported accessor used by Go templates.
func (f flagDef) ResolvedType() string { return f.resolvedType() }

// argumentDef represents an argument definition for code generation.
type argumentDef struct {
	Name      string
	Type      string // any valid Go type string
	Required  bool
	Variadic  bool     // inferred from schema type being array
	Default   string   // default value (applied when arg not provided)
	ConfigKey string   // config file dot-path key (checked when arg not provided via CLI or env)
	EnvVarKey string   // environment variable name (checked when arg not provided via CLI)
	Enum      []string // allowed values; auto-validated at parse time
	Pattern   string   // regex pattern; auto-validated at parse time (string type only)
	Nullable  bool     // when true, generates *T field type instead of T
}

// resolvedType returns the argument's type, defaulting to "string" if unset.
func (a argumentDef) resolvedType() string {
	if a.Type == "" {
		return "string"
	}
	return a.Type
}

// ResolvedType is the exported accessor used by Go templates.
func (a argumentDef) ResolvedType() string { return a.resolvedType() }

// binConfig holds build metadata configuration for the CLI binary.
// When Name is non-empty, the generated buildinfo code produces PascalCase
// ldflags-settable variables (e.g., Name="Rotini" → RotiniVersion, RotiniCommit,
// RotiniBuildTime) and a BuildInfo() accessor.
type binConfig struct {
	Name        string // PascalCase binary name, used as ldflags variable prefix
	DisplayName string // original binary name as declared in the spec (e.g. "rotini"), used for help output
}

// configDef represents a config file definition for code generation.
type configDef struct {
	Name              string             // logical name (e.g., "app-config")
	Path              string             // file path (supports ~ for home dir)
	Format            string             // "json" (default), "yaml", or "toml"
	Schema            []responseFieldDef // sorted fields from schema.properties (when schema is set)
	SchemaRequired    []string           // required field names from schema.required
	HotReloadDebounce time.Duration      // debounce window (default: 200ms)
}

// flattenedCommandEntry holds a single command path and its definition,
// for use in the combined rotini.gen.go.tmpl per-command range loop.
type flattenedCommandEntry struct {
	CommandPath string
	Command     commandDef
}

// generateResult captures the result of a generate or initialize operation.
type generateResult struct {
	InitFilesCreated    []string // init files written (definition, configuration, main.go, etc.)
	InitFilesSkipped    []string // init files skipped (already exist and force is false)
	GenFilesCreated     []string // gen .gen.go files newly created
	GenFilesModified    []string // gen .gen.go files rewritten with changed content
	GenFilesRemoved     []string // stale gen files cleaned up
	GenFilesUnchanged   []string // gen files whose content was identical (not rewritten)
	ImplFilesCreated    []string // new impl stubs created
	ImplFilesRemoved    []string // orphaned impl files deleted
	ImplFilesSkipped    []string // existing impl files left untouched
	HandlersRegenerated bool     // whether handlers.go was actually rewritten (content changed)
}

// codegenOptions configures generateProgramFile and GenerateModelsFile.
type codegenOptions struct {
	PackageName        string             // Go package name for generated code
	OutputDir          string             // directory for .gen.go files
	OutputFile         string             // output filename (default: "rotini.gen.go")
	Merged             bool               // when true, framework types live in the same package as handler stubs (Cases 2 and 3)
	SameFile           bool               // when true, the Handlers rollup is embedded in the framework file (Case 3)
	Commands           []commandDef       // top-level commands
	RootFlags          []flagDef          // root-level flags
	RootStdin          *stdinDefSpec      // root-level stdin spec (nil when root has no stdin)
	ConfigurationFiles []configDef        // config file definitions
	RemoteCommands     []remoteCommandDef // remote command definitions
	AllEvents          []eventDef         // all events from root + entire command tree (flat, dedup by key)
	Bin                binConfig          // build metadata config
	Imports            []string           // user-provided Go import paths for custom types
	Timeout            time.Duration      // default timeout applied to all commands
	Metadata           []metadataDef      // build-time ldflag var definitions
	AllCustomTypes     []string           // deduplicated list of custom (non-primitive) type names across all flags/args
}

// convertedDefinition holds all codegen-ready data converted from a spec definition.
type convertedDefinition struct {
	Commands           []commandDef
	RootFlags          []flagDef
	RootStdin          *stdinDefSpec
	ConfigurationFiles []configDef
	RemoteCommands     []remoteCommandDef
	AllEvents          []eventDef
	Timeout            time.Duration
	AllCustomTypes     []string // deduplicated custom (non-primitive) type names across all flags/args
}

// writeCommandMetaFields writes CommandMeta struct fields into sb at the given indent level.
// This is the shared field-writing logic used by renderCommandMetaLiteral.
func writeCommandMetaFields(sb *strings.Builder, cmd commandDef, indent string) {
	fmt.Fprintf(sb, "%sName:        %q,\n", indent, cmd.Name)

	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(sb, "%sAliases:     []string{%s},\n", indent, quoteSlice(cmd.Aliases))
	}

	if len(cmd.Flags) > 0 {
		sb.WriteString(indent + "Flags: []rr.RotiniFlag{\n")
		for _, f := range cmd.Flags {
			sb.WriteString(renderFlagMetaLiteral(f, indent+"\t"))
		}
		sb.WriteString(indent + "},\n")
	}

	if len(cmd.Arguments) > 0 {
		sb.WriteString(indent + "Arguments: []rr.RotiniArgument{\n")
		for _, a := range cmd.Arguments {
			sb.WriteString(renderArgumentMetaLiteral(a, indent+"\t"))
		}
		sb.WriteString(indent + "},\n")
	}

	if len(cmd.Commands) > 0 {
		sb.WriteString(indent + "Commands: []rr.RotiniCommand{\n")
		for _, sub := range cmd.Commands {
			sb.WriteString(renderCommandMetaLiteral(sub, indent+"\t"))
		}
		sb.WriteString(indent + "},\n")
	}

	if len(cmd.RemoteCommands) > 0 {
		sb.WriteString(indent + "RemoteCommands: []rr.RotiniRemoteCommands{\n")
		for _, rc := range cmd.RemoteCommands {
			fmt.Fprintf(sb, "%s\t{Name: %q, Aliases: []string{%s}, Timeout: %d},\n",
				indent, rc.Name, quoteSlice(rc.Aliases), rc.Timeout)
		}
		sb.WriteString(indent + "},\n")
	}
}

// renderCommandMetaLiteral renders an inline CommandMeta literal (for nested subcommands).
func renderCommandMetaLiteral(cmd commandDef, indent string) string {
	var sb strings.Builder
	sb.WriteString(indent + "{\n")
	writeCommandMetaFields(&sb, cmd, indent+"\t")
	sb.WriteString(indent + "},\n")
	return sb.String()
}

// renderFlagMetaLiteral renders a single FlagMeta struct literal.
func renderFlagMetaLiteral(f flagDef, indent string) string {
	var sb strings.Builder
	sb.WriteString(indent + "{")
	fmt.Fprintf(&sb, "Name: %q", f.Name)

	if len(f.Identifiers) > 0 {
		fmt.Fprintf(&sb, ", Identifiers: []string{%s}", quoteSlice(f.Identifiers))
	}

	fmt.Fprintf(&sb, ", Type: %q", f.resolvedType())

	if f.Required {
		sb.WriteString(", Required: true")
	}

	if f.Default != "" {
		fmt.Fprintf(&sb, ", Default: %q", f.Default)
	}

	if len(f.Enum) > 0 {
		fmt.Fprintf(&sb, ", Enum: []string{%s}", quoteSlice(f.Enum))
	}

	if f.Min != nil {
		fmt.Fprintf(&sb, ", Min: rr.Float64Ptr(%g)", *f.Min)
	}

	if f.Max != nil {
		fmt.Fprintf(&sb, ", Max: rr.Float64Ptr(%g)", *f.Max)
	}

	if f.MinLength != nil {
		fmt.Fprintf(&sb, ", MinLength: rr.IntPtr(%d)", *f.MinLength)
	}

	if f.MaxLength != nil {
		fmt.Fprintf(&sb, ", MaxLength: rr.IntPtr(%d)", *f.MaxLength)
	}

	if f.MinItems != nil {
		fmt.Fprintf(&sb, ", MinItems: rr.IntPtr(%d)", *f.MinItems)
	}

	if f.MaxItems != nil {
		fmt.Fprintf(&sb, ", MaxItems: rr.IntPtr(%d)", *f.MaxItems)
	}

	if f.Pattern != "" {
		fmt.Fprintf(&sb, ", Pattern: %q", f.Pattern)
	}

	if f.EnvVarKey != "" {
		fmt.Fprintf(&sb, ", EnvVarKey: %q", f.EnvVarKey)
	}

	if f.ConfigKey != "" {
		fmt.Fprintf(&sb, ", ConfigKey: %q", f.ConfigKey)
	}

	if f.Nullable {
		sb.WriteString(", Nullable: true")
	}

	if f.EnvOnly {
		sb.WriteString(", EnvOnly: true")
	}

	sb.WriteString("},\n")
	return sb.String()
}

// renderArgumentMetaLiteral renders a single ArgumentMeta struct literal.
func renderArgumentMetaLiteral(a argumentDef, indent string) string {
	var sb strings.Builder
	sb.WriteString(indent + "{")
	fmt.Fprintf(&sb, "Name: %q", a.Name)

	fmt.Fprintf(&sb, ", Type: %q", a.resolvedType())

	if a.Default != "" {
		fmt.Fprintf(&sb, ", Default: %q", a.Default)
	}

	if a.Required {
		sb.WriteString(", Required: true")
	}

	if a.Variadic {
		sb.WriteString(", Variadic: true")
	}

	if len(a.Enum) > 0 {
		fmt.Fprintf(&sb, ", Enum: []string{%s}", quoteSlice(a.Enum))
	}

	if a.Pattern != "" {
		fmt.Fprintf(&sb, ", Pattern: %q", a.Pattern)
	}

	if a.EnvVarKey != "" {
		fmt.Fprintf(&sb, ", EnvVarKey: %q", a.EnvVarKey)
	}

	if a.ConfigKey != "" {
		fmt.Fprintf(&sb, ", ConfigKey: %q", a.ConfigKey)
	}

	if a.Nullable {
		sb.WriteString(", Nullable: true")
	}

	sb.WriteString("},\n")
	return sb.String()
}

// quoteSlice produces a comma-separated list of quoted strings for Go source.
func quoteSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// binaryVersion returns the rotini binary version.
// It falls back to Go module version from runtime/debug.ReadBuildInfo().
func binaryVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// validateSpecFileKeys checks that the required top-level keys exist in the spec file.
func validateSpecFileKeys(path string) error {
	return validateFileKeys(path, []string{"name"})
}

// validateConfFileKeys checks that the required top-level keys exist in the conf file.
func validateConfFileKeys(path string) error {
	return validateFileKeys(path, []string{"generate"})
}

// validateFileKeys checks that the given required keys exist in a YAML or JSON file.
func validateFileKeys(path string, required []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(path))
	isYAML := ext == ".yaml" || ext == ".yml"

	var keys map[string]any
	if isYAML {
		if err := yaml.Unmarshal(data, &keys); err != nil {
			return err
		}
	} else {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		keys = make(map[string]any, len(raw))
		for k := range raw {
			keys[k] = struct{}{}
		}
	}

	for _, key := range required {
		if _, exists := keys[key]; !exists {
			return fmt.Errorf("required key '%s' not found", key)
		}
	}
	return nil
}

// validateConfigurationStructure ensures the configuration has required fields.
func validateConfigurationStructure(config *configuration) error {
	if config.Generate.Cmd.Package == "" {
		return fmt.Errorf("configuration.generate.cmd.package is required")
	}
	if config.Generate.Framework.Package == "" {
		return fmt.Errorf("configuration.generate.framework.package is required")
	}
	if config.Generate.Cmd.GenFile == "" {
		return fmt.Errorf("configuration.generate.cmd.gen_file is required")
	}
	if !strings.HasSuffix(config.Generate.Cmd.GenFile, ".go") {
		return fmt.Errorf("configuration.generate.cmd.gen_file must end in .go")
	}
	if config.Generate.Framework.GenFile == "" {
		return fmt.Errorf("configuration.generate.framework.gen_file is required")
	}
	if !strings.HasSuffix(config.Generate.Framework.GenFile, ".go") {
		return fmt.Errorf("configuration.generate.framework.gen_file must end in .go")
	}
	return nil
}

// validateSchemaVersion checks that the version embedded in the $schema URL matches the rotini binary version.
// The check is only enforced when the schema URL pins a release version (e.g. v1.0.0).
// If the schema has no version or a non-release version (pseudo-version, "dev"), the check is skipped.
func validateSchemaVersion(schema string) error {
	ver := schemaVersion(schema)
	if ver == "" || !isReleaseVersion(ver) {
		// Schema does not pin a release version; skip enforcement.
		return nil
	}
	binaryVer := binaryVersion()
	if ver != binaryVer {
		return fmt.Errorf("$schema version %q does not match rotini binary version %q; update $schema to match installed binary version", ver, binaryVer)
	}
	return nil
}

// schemaVersion extracts the rotini version embedded in a $schema URL.
// Supported URL formats:
//
//	https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/{VERSION}/schema-spec.json
//	https://raw.githubusercontent.com/matthewgetz/rotini/{VERSION}/schema-spec.json
//
// Also accepts the legacy rotini-schema.json suffix for backward compatibility.
func schemaVersion(schemaURL string) string {
	const prefix = "rotini/"
	suffixes := []string{"/schema-spec.json", "/schema-conf.json", "/rotini-schema.json"}
	start := strings.Index(schemaURL, prefix)
	if start == -1 {
		return ""
	}
	tail := schemaURL[start+len(prefix):]
	for _, suffix := range suffixes {
		end := strings.Index(tail, suffix)
		if end != -1 {
			ver := tail[:end]
			ver = strings.TrimPrefix(ver, "refs/tags/")
			return ver
		}
	}
	return ""
}

// isReleaseVersion reports whether version is a semver release (e.g. v1.0.0).
// Pseudo-versions (v0.0.0-YYYYMMDD...) and "dev" are not considered release versions.
func isReleaseVersion(version string) bool {
	patch := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(patch) != 3 {
		return false
	}
	return !strings.ContainsAny(patch[2], "-+")
}
