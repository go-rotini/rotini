package codegen

// This file owns loading and compiling the embedded rotini JSON Schemas the validate
// stage uses (each compiled once and cached). NewProcessor compiles both up front and
// holds them; validateSpec/validateConf judge the reconciled documents against them.
// Reading the end-user's documents lives in reconcile_reader.go / reconcile.go.

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/go-rotini/jsonschema"
)

var (
	//go:embed schema-spec.json
	schemaSpecFileBytes []byte
	//go:embed schema-conf.json
	schemaConfFileBytes []byte
	//go:embed schema-contract.json
	schemaContractFileBytes []byte
	//go:embed schema-error.json
	errorSchemaBytes []byte
)

// The embedded rotini JSON Schemas are immutable, so each is compiled at most
// once per process and the result cached — the cache spares the recompile when
// a fresh Processor is built per invocation (NewProcessor calls these).
var (
	// loadSpecSchema compiles the embedded spec schema once and returns the
	// cached result.
	loadSpecSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
		return compileSchema("spec", schemaSpecFileBytes)
	})

	// loadConfSchema compiles the embedded conf schema once and returns the
	// cached result.
	loadConfSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
		return compileSchema("conf", schemaConfFileBytes)
	})
)

// compileSchema compiles one embedded rotini JSON Schema, labeling a failure
// with the schema kind ("spec"/"conf"). The embedded schemas are fixed at build
// time, so an error here is a rotini packaging bug, not user input.
func compileSchema(kind string, schemaBytes []byte) (*jsonschema.Schema, error) {
	schema, err := jsonschema.Compile(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("compile embedded %s schema: %w", kind, err)
	}
	return schema, nil
}
