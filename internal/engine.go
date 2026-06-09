package internal

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/go-rotini/jsonschema"

	_ "embed"
)

//go:embed schema-spec.json
var specSchemaBytes []byte

//go:embed schema-conf.json
var confSchemaBytes []byte

// loadSpecSchema compiles the embedded spec JSON Schema once and returns the cached
// result.

// loadConfSchema compiles the embedded conf JSON Schema once and returns the cached
// result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(confSchemaBytes)
		if err != nil {
			errConfSchema = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		confSchema = s
	})
	return confSchema, errConfSchema
}

// errSpecPathRequired is reported when no spec-file path is supplied.
var errSpecPathRequired = errors.New("spec file path is required")

// Overrides are the flag-derived conf settings the Loader overlays onto the
// resolved conf, so every downstream stage reads a single, final Conf rather than
// reconciling flags against file contents itself.
type Overrides struct {
	FailMode string // --fail; overrides conf.validate.fail when non-empty
}

// applyTo overlays the non-empty overrides onto conf in place.
func (o Overrides) applyTo(conf *Conf) {
	if o.FailMode != "" {
		if conf.Validate == nil {
			conf.Validate = &ValidateConfig{}
		}
		conf.Validate.Fail = o.FailMode
	}
}

// Input is a loaded rotini document — its resolved path and decoded value. The
// Loader produces one per document: an Input[Spec] and an Input[Conf]. The zero
// value (empty Path, nil Doc) represents "not present" — e.g. an optional conf that
// wasn't found, where defaults are used.
type Input[T any] struct {
	Path string // resolved path to the document; "" when none was found
	Doc  *T     // the decoded document (with defaults applied / overrides overlaid, for conf)
}

// Loader is the first stage of every codegen workflow: it resolves and reads a
// rotini spec and its conf into a typed model — finding the files at their default
// locations when explicit paths aren't given, applying the built-in conf defaults,
// and overlaying any flag-derived overrides.
type Loader struct {
}

// NewLoader constructs a Loader with the default file-resolution policy.
func NewLoader() *Loader {
	return &Loader{}
}

// LoadSpec resolves and reads the spec file into an Input. The spec is required.
//
// For now it reads the given path directly; a later increment adds the
// default-location fallback (discovering a .rotini.spec.* when path is empty).
func (l *Loader) LoadSpec(path string) (Input[Spec], error) {
	if path == "" {
		return Input[Spec]{}, errSpecPathRequired
	}
	spec, err := ReadSpec(path)
	if err != nil {
		return Input[Spec]{}, err
	}
	return Input[Spec]{Path: path, Doc: spec}, nil
}

// LoadConf resolves and reads the conf into an Input, then overlays the flag
// overrides. The conf is optional: when no path is given and none is discovered
// beside the spec (or the resolved path does not exist), a default Conf is used and
// the returned Input.Path is "".
//
// It takes the already-loaded spec because conf resolution depends on it — the
// spec's path locates a conf alongside it, and (in a later increment) the spec's
// root name drives the built-in package defaults.
func (l *Loader) LoadConf(spec Input[Spec], confPath string, overrides Overrides) (Input[Conf], error) {
	confPath = ResolveConfPath(spec.Path, confPath)

	conf := &Conf{} // default when no conf is present
	if confPath != "" {
		if _, statErr := os.Stat(confPath); statErr == nil {
			c, err := ReadConf(confPath)
			if err != nil {
				return Input[Conf]{}, err
			}
			conf = c
		} else if os.IsNotExist(statErr) {
			confPath = "" // resolved path does not exist → fall back to defaults
		} else {
			return Input[Conf]{}, fmt.Errorf("stat conf %s: %w", confPath, statErr)
		}
	}

	overrides.applyTo(conf)

	// TODO (next increment): apply the built-in conf defaults — the cmd/<root>/cli
	// package paths, gen file names, and feature dirs — keyed off the spec's root
	// name (spec.Doc.Command.Name). Open question: does that package-path defaulting
	// belong here (Loader yields a fully-defaulted Conf) or in the Generator (which
	// is what actually consumes those paths)?

	return Input[Conf]{Path: confPath, Doc: conf}, nil
}

// Engine is the top-level codegen orchestrator. It holds the loaded spec and conf
// (each as its own Input — see Loader) plus the binary version for the $schema
// guard, and drives the pipeline stages over them — Validate, Lint, Generate —
// plus Initialize, which scaffolds a new rotini-powered CLI.
type Engine struct {
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	errSpecSchema  error
	spec           *Spec

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	errConfSchema  error
	conf           *Conf

	version string // the running binary's version string, for the $schema guard
}

func (e *Engine) loadSpecSchema() (*jsonschema.Schema, error) {
	e.specSchemaOnce.Do(func() {
		schema, err := jsonschema.Compile(specSchemaBytes)
		if err != nil {
			e.errSpecSchema = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		e.specSchema = schema
	})
	return e.specSchema, e.errSpecSchema
}

func (e *Engine) loadConfSchema() (*jsonschema.Schema, error) {
	e.confSchemaOnce.Do(func() {
		schema, err := jsonschema.Compile(specSchemaBytes)
		if err != nil {
			e.errConfSchema = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		e.confSchema = schema
	})
	return e.confSchema, e.errConfSchema
}

// Validate schema-validates the loaded spec and conf against the embedded rotini
// schemas, including the $schema↔binary-version guard.
func (e *Engine) Validate() error {
	// TODO: delegate to the Validator over e.spec and e.conf.
	return nil
}

// Lint runs the semantic lints over the loaded spec and conf.
func (e *Engine) Lint() error {
	// TODO: delegate to the Linter over e.spec and e.conf.
	return nil
}

// Generate emits the program from the loaded spec and conf — the cli and cligen
// packages plus the enabled doc features (help/man/markdown/completion).
func (e *Engine) Generate() error {
	// TODO: delegate to the Generator over e.spec and e.conf.
	return nil
}

// Initialize scaffolds a new rotini CLI (spec, conf, main.go, and the cli/cligen
// gen files) and generates it.
func (e *Engine) Initialize() error {
	// TODO: delegate to the Initializer.
	return nil
}
