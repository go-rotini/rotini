package internal

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type fileFormat int

const (
	formatUnknown fileFormat = iota
	formatYAML
	formatJSON
	formatJSONC
	formatTOML
)

var errUnsupportedFormat = errors.New("unsupported file format")

func detectFileFormat(path string) fileFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return formatYAML
	case ".json":
		return formatJSON
	case ".jsonc":
		return formatJSONC
	case ".toml":
		return formatTOML
	default:
		return formatUnknown
	}
}

// readFile reads the file at path and decodes it into a value of type T,
// choosing the decoder from the file extension. YAML, JSON, and JSONC all
// honor the json struct tags carried by the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	out := new(T)
	switch format {
	case formatYAML:
		err = yaml.Unmarshal(data, out)
	case formatJSON:
		err = json.Unmarshal(data, out)
	case formatJSONC:
		err = jsonc.Unmarshal(data, out)
	case formatTOML:
		err = toml.Unmarshal(data, out)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}

// writeFile encodes v in the format selected from path's extension and
// writes it atomically, creating parent directories as needed. JSONC files
// are written as standard JSON, which is a valid JSONC document.
func writeFile[T any](path string, v *T) error {
	var (
		data []byte
		err  error
	)
	switch detectFileFormat(path) {
	case formatYAML:
		data, err = yaml.Marshal(v)
	case formatJSON, formatJSONC:
		data, err = json.MarshalIndent(v, "", "  ")
		data = append(data, '\n')
	case formatTOML:
		data, err = toml.Marshal(v)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	if err := fs.WriteFile(path, data, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// toJSON reads the file at path and returns its contents as canonical JSON
// bytes, regardless of the source serialization. It feeds documents to the
// jsonschema validator, which operates on JSON instances. The raw instance
// is returned (not a decoded struct) so schema rules like
// additionalProperties:false still see unknown fields.
func toJSON(path string) ([]byte, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	switch format {
	case formatJSON:
		return data, nil
	case formatJSONC:
		out, err := jsonc.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("convert %s to json: %w", path, err)
		}
		return out, nil
	case formatYAML:
		out, err := yaml.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("convert %s to json: %w", path, err)
		}
		return out, nil
	case formatTOML:
		out, err := toml.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("convert %s to json: %w", path, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
}

// readSpec reads and decodes the rotini spec file at path. The serialization
// (YAML, JSON, or JSONC) is selected from the file extension. It does not
// validate the document against the spec schema; use [Validate] for that.
// ReadSpec is the exported read seam the codegen package's Loader uses to decode a
// spec across the package boundary; internal's own code uses the unexported
// readSpec. (Migration bridge: when the read path moves fully into codegen this
// goes away.)
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// writeSpec encodes s and writes it to path, selecting the serialization from
// the file extension.
func writeSpec(path string, s *Spec) error {
	return writeFile(path, s)
}

// readConf reads and decodes the rotini conf file at path. The serialization
// (YAML, JSON, or JSONC) is selected from the file extension. It does not
// validate the document against the conf schema; use [Validate] for that.
// ReadConf is the exported read seam the codegen package's Loader uses to decode a
// conf across the package boundary; internal's own code uses the unexported
// readConf. (Migration bridge: when the read path moves fully into codegen this
// goes away.)
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// writeConf encodes c and writes it to path, selecting the serialization from
// the file extension.
func writeConf(path string, c *Conf) error {
	return writeFile(path, c)
}

// errSpecPathRequired is reported when no spec-file path is supplied and none of
// the fallback locations resolve to a spec.
var errSpecPathRequired = errors.New("spec file path is required")

// processor owns every rotini process end-to-end — schema-file loading, end-user
// spec/conf resolution + reading, validation, generation, and initialization. One
// value carries the compiled schemas and the decoded documents through a run.
type processor struct {
	version  string // running binary's version string, for the $schema guard
	specPath string // explicit spec path ("" → resolved from the fallback locations)
	confPath string // explicit conf path ("" → resolved beside the spec, else defaults)
	failMode string // --fail override; "" → the conf's validate.fail (then "collect")

	schemaSpec *jsonschema.Schema
	spec       *Spec

	schemaConf *jsonschema.Schema
	conf       *Conf
}

//go:embed schema-spec.json
var schemaSpecBytes []byte

//go:embed schema-conf.json
var schemaConfBytes []byte

// The embedded rotini JSON Schemas are immutable, so each is compiled at most once
// per process and the result cached. NewProcessor fills a processor's schema fields
// from these — the cache spares the recompile when a fresh processor is built per
// pass (e.g. watch mode rebuilds one on every change).
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	specSchemaErr  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	confSchemaErr  error
)

// compileSchema compiles a rotini JSON Schema, tagging a failure with the schema's
// name ("spec" or "conf").
func compileSchema(name string, schemaBytes []byte) (*jsonschema.Schema, error) {
	schema, err := jsonschema.Compile(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("compile %s schema: %w", name, err)
	}
	return schema, nil
}

// loadSpecSchema compiles the embedded spec schema once and returns the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() { specSchema, specSchemaErr = compileSchema("spec", schemaSpecBytes) })
	return specSchema, specSchemaErr
}

// loadConfSchema compiles the embedded conf schema once and returns the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() { confSchema, confSchemaErr = compileSchema("conf", schemaConfBytes) })
	return confSchema, confSchemaErr
}

// NewProcessor builds a processor for the spec at specFilePath and the conf at
// confFilePath (either may be empty — the loader phase resolves them against the
// .rotini.{spec,conf}.* fallback locations beside the spec), tagged with version
// (the running binary's version string for the $schema guard; "" → guard skipped).
// It compiles both embedded rotini JSON Schemas up front.
func NewProcessor(specFilePath, confFilePath, version string) (*processor, error) {
	schemaSpec, err := loadSpecSchema()
	if err != nil {
		return nil, err
	}
	schemaConf, err := loadConfSchema()
	if err != nil {
		return nil, err
	}
	return &processor{
		version:    version,
		specPath:   specFilePath,
		confPath:   confFilePath,
		schemaSpec: schemaSpec,
		schemaConf: schemaConf,
	}, nil
}

// getFallbackPaths returns the default discovery locations for a spec or conf file
// (fileType is "spec" or "conf") within dir, in extension-precedence order. The
// loader passes dir = the spec's directory, so the conf is discovered beside the spec.
func getFallbackPaths(dir, fileType string) []string {
	exts := []string{"yaml", "toml", "json", "jsonc"}
	paths := make([]string, len(exts))
	for i, ext := range exts {
		paths[i] = filepath.Join(dir, fmt.Sprintf(".rotini.%s.%s", fileType, ext))
	}
	return paths
}

// firstExisting returns the first path in paths that exists on disk, or "" if none do.
func firstExisting(paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// resolveSpecPath resolves the spec file path: the explicit specPath when given,
// otherwise the first .rotini.spec.* in the working directory, or "" when none is
// found (callers treat that as the required-spec error).
func resolveSpecPath(specPath string) (string, error) {
	if specPath != "" {
		return specPath, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return firstExisting(getFallbackPaths(cwd, "spec")), nil
}

// resolveConfBesideSpec resolves the conf file path: the explicit confPath when
// given, otherwise the first .rotini.conf.* beside the spec, or "" when none is found
// (callers fall back to a default Conf).
func resolveConfBesideSpec(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	return firstExisting(getFallbackPaths(filepath.Dir(specPath), "conf"))
}

// load resolves and reads the end-user spec and conf, decoding them onto the
// processor. The spec is loaded first because conf resolution looks beside it.
func (p *processor) load() error {
	if err := p.loadSpec(); err != nil {
		return err
	}
	return p.loadConf()
}

// loadSpec resolves the spec path — the explicit path passed to NewProcessor, else
// the first .rotini.spec.* in the working directory — then reads and decodes it onto
// the processor. The spec is required: no explicit path and no fallback match is an
// error.
func (p *processor) loadSpec() error {
	path, err := resolveSpecPath(p.specPath)
	if err != nil {
		return err
	}
	if path == "" {
		return errSpecPathRequired
	}

	spec, err := readSpec(path)
	if err != nil {
		return err
	}
	p.specPath = path
	p.spec = spec
	return nil
}

// loadConf resolves the conf path — the explicit path passed to NewProcessor, else
// the first .rotini.conf.* beside the resolved spec — then reads and decodes it onto
// the processor. The conf is optional: no explicit path and no fallback match yields
// a default Conf. An explicit path that does not exist is a read error.
func (p *processor) loadConf() error {
	path := resolveConfBesideSpec(p.specPath, p.confPath)

	// The conf is optional: no resolved path — or a resolved (explicit) path that
	// doesn't exist — falls back to a default Conf, with confPath cleared so the
	// validator skips it.
	if path == "" {
		p.confPath = ""
		p.conf = &Conf{}
		return nil
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			p.confPath = ""
			p.conf = &Conf{}
			return nil
		}
		return fmt.Errorf("stat conf %s: %w", path, statErr)
	}

	conf, err := readConf(path)
	if err != nil {
		return err
	}
	p.confPath = path
	p.conf = conf
	return nil
}

// validate runs the validator phase over the loaded spec and conf (call load
// first): it schema-validates each document on its raw JSON instance, enforces the
// $schema↔version guard, and runs the rotini-specific rules the JSON Schema can't
// express. Problems are aggregated via errors.Join, or — when the loaded conf
// selects fast mode (validate.fail = "fast") — the first problem is returned. It
// returns nil when the spec and conf are valid.
func (p *processor) validate() error {
	fast := p.failFast()

	problems := p.validateSpec()
	if fast && len(problems) > 0 {
		return problems[0]
	}

	problems = append(problems, p.validateConf()...)
	if fast && len(problems) > 0 {
		return problems[0]
	}
	return errors.Join(problems...)
}

// validateSpec schema-validates the spec on its raw JSON instance, then — only when
// it is schema-valid — runs the rotini-specific spec rules and the $schema version
// guard. A schema violation short-circuits the rules: linting a malformed document
// is meaningless and would pile errors onto an already-broken file.
func (p *processor) validateSpec() []error {
	if problems := validateDocument(p.specPath, "spec", p.schemaSpec); len(problems) > 0 {
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(p.spec)...)
	}
	if err := checkSchemaVersion("spec", p.spec.Schema, p.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// validateConf schema-validates the conf on its raw JSON instance when one was
// resolved, then guards its $schema version. The conf is optional: when none was
// found (a default Conf, empty confPath) there is nothing to validate. The conf-side
// rotini-specific rules are a seam — none exist yet.
func (p *processor) validateConf() []error {
	if p.confPath == "" {
		return nil
	}
	if problems := validateDocument(p.confPath, "conf", p.schemaConf); len(problems) > 0 {
		return problems
	}
	if err := checkSchemaVersion("conf", p.conf.Schema, p.version); err != nil {
		return []error{err}
	}
	return nil
}

// failFast reports whether validation should stop at the first problem. The --fail
// override (p.failMode) wins; otherwise the loaded conf's validate.fail is used. Only
// "fast" enables it — anything else collects every problem (the default).
func (p *processor) failFast() bool {
	mode := p.failMode
	if mode == "" && p.conf != nil && p.conf.Validate != nil {
		mode = p.conf.Validate.Fail
	}
	return mode == "fast"
}

// generate runs the generator phase over the loaded spec and conf. Call load — and,
// through the workflow, validate — first: invalid input must never reach codegen. It
// applies the built-in conf defaults (the cmd/<root>/cli package paths, gen file
// names, and feature dirs, keyed off the spec's root command name), then emits the
// program: the cli and cligen packages plus the enabled doc features
// (help/man/markdown/completion).
func (p *processor) generate() error {
	applyConfDefaults(p.conf, p.spec.Command.Name)
	return generateAll(p.spec, p.conf, p.specPath)
}

// recipe selects the project layout the initializer scaffolds.
type recipe string

const (
	// recipeCmd scaffolds the CLI under cmd/<name>/ with its generated package
	// beside it: cmd/<name>/{.rotini.spec,.rotini.conf,main.go} plus
	// cmd/<name>/cli/{rotini.gen.go, per-command stubs}. The established layout and
	// the default.
	recipeCmd recipe = "cmd"
	// recipeFlat scaffolds everything into the current module's main (root) package
	// — main.go, the handler files, and the codegen together (an existing module; it
	// does not bootstrap go.mod/go.sum). Not yet implemented.
	recipeFlat recipe = "flat"
)

// initialize scaffolds a new rotini CLI named name using the given recipe, then
// generates it. format selects the spec/conf serialization (yaml/jsonc/json; empty →
// the module conf's initialize.format, then yaml); force overwrites the create-once
// files (spec/conf/main.go); into, when set, registers the new CLI as a $ref
// sub-command of an existing one. The running binary's version (p.version) is stamped
// into the scaffolded $schema URLs.
//
// For now the cmd recipe (and the empty default) delegate to the established
// scaffolding path so the output is unchanged; the flat recipe is a seam to be built
// once its layout is settled.
func (p *processor) initialize(name, format string, force bool, into string, rcp recipe) error {
	switch rcp {
	case recipeCmd, "":
		return p.initializeCmd(name, format, force, into)
	case recipeFlat:
		return fmt.Errorf("the %q init recipe is not yet supported", recipeFlat)
	default:
		return fmt.Errorf("unknown init recipe %q (want %q or %q)", rcp, recipeCmd, recipeFlat)
	}
}

// validatePass runs one pass of the validate workflow: load, then validate. It is
// the unit the run/watch engine repeats — each pass builds a fresh processor, so a
// re-read picks up edits in watch mode.
func (p *processor) validatePass() error {
	if err := p.load(); err != nil {
		return err
	}
	return p.validate()
}

// generatePass runs one pass of the generate workflow: load, validate, then
// generate. Validation is the gate — when the spec or conf is invalid it returns
// that error and generation never runs, so invalid input never reaches codegen.
func (p *processor) generatePass() error {
	if err := p.load(); err != nil {
		return err
	}
	if err := p.validate(); err != nil {
		return err
	}
	return p.generate()
}

// runProcessorWorkflow runs a processor workflow once, or — when watch is set —
// re-runs it whenever the spec or conf changes, until interrupted (ctrl-c). It
// resolves the spec and conf paths up-front so watch mode watches exactly the files
// the processor reads, then drives the shared run/watch engine: each pass builds a
// fresh processor (re-reading the files, so edits are picked up) and runs pass over
// it, stamped with a "[HH:MM:SS] <took>" summary handed to onResult.
//
// Without watch it returns the single pass's error (not routed through onResult), so
// the caller can treat the run as failed; with watch every pass — success or failure
// — goes to onResult and only a failure to start watching is returned. pass is a
// method expression: (*processor).validatePass or (*processor).generatePass. failMode
// is the --fail override carried onto each pass's processor (generate passes "").
func runProcessorWorkflow(specPath, confPath, version, failMode string, watch bool, pass func(*processor) error, onResult func(result string, err error)) error {
	resolvedSpec, err := resolveSpecPath(specPath)
	if err != nil {
		return err
	}
	if resolvedSpec == "" {
		return errSpecPathRequired
	}
	resolvedConf := resolveConfBesideSpec(resolvedSpec, confPath)

	timed := func() (string, error) {
		start := time.Now()
		p, err := NewProcessor(resolvedSpec, resolvedConf, version)
		if err == nil {
			p.failMode = failMode
			err = pass(p)
		}
		return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

/*
 * Processor functionality:
 * Loader
 * 1. compile schema spec
 * 2. compile schema conf
 * 3. get the end-users spec file bytes
 *   a. first by path passed in param
 *   b. next by looking at the fallback paths (if any of the fallback paths resolve to a spec, use it -- if none do, err)
 * 4. get the end-users conf file bytes -- first by path passed in param, then by looking at the fallback paths (if any of the fallback paths resolve to a conf, use it -- if none do, err)
 *   a. first by path passed in param
 *   b. next by looking at the fallback paths (if any of the fallback paths resolve to a conf, use it -- if none do, do not err, go to next)
 *   c. finally, fallback to a "default conf" shape
 * Validator
 * 1. ensures end-user spec file satisfies schema spec
 * 2. ensures end-user conf file satisfies schema conf
 * 3. ensures "rotini-specific rules" for end-user spec file all okay -- no dupe command names/aliases, etc -- whatever won't be "caught" by the jsonschema check
 * 4. ensures "rotini-specific rules" for end-user conf file all okay -- whatever won't be "caught" by the jsonschema check
 * Generator
 * 1. generator for help
 * 2. generator for man
 * 3. generator for markdown
 * 4. generator for completion
 * 5. generator for rotini codegen
 * Initializer
 * 1. scaffolds based on a "recipe" type
 *   a. "flat" = go.mod, go.sum, main.go, commands handler files, codegen files all in main package
 *   b. "cmd" = ./cmd/<root command name>/main.go, ./internal/<root command name>/clipkg (see conf file)
 */
