package internal

import (
	"fmt"
	"sync"

	"github.com/go-rotini/jsonschema"

	_ "embed"
)

//go:embed schema-spec.json
var specSchemaBytes []byte

//go:embed schema-conf.json
var confSchemaBytes []byte

var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	errSpecSchema  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	errConfSchema  error
)

// Engine is the top-level codegen orchestrator. It holds the loaded spec and conf
// (each as its own Input — see Loader) plus the binary version for the $schema
// guard, and drives the pipeline stages over them — Validate, Lint, Generate —
// plus Initialize, which scaffolds a new rotini-powered CLI.
type Engine struct {
	specSchema *jsonschema.Schema
	confSchema *jsonschema.Schema
	spec       Input[Spec] // the loaded spec
	conf       Input[Conf] // the loaded conf; zero value when no conf was found
	version    string      // the running binary's version string, for the $schema guard
}

func (e *Engine) loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() {
		schema, err := jsonschema.Compile(specSchemaBytes)
		if err != nil {
			errSpecSchema = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		specSchema = schema
	})
	return specSchema, errSpecSchema
}

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
