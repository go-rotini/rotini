package codegen

// The embedded rotini JSON Schemas and their compilation for the validate stage.

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

// loadSpecSchema and loadConfSchema compile an embedded schema at most once per process,
// so each new Processor reuses the result.
var (
	loadSpecSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
		return compileSchema("spec", schemaSpecFileBytes)
	})

	loadConfSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
		return compileSchema("conf", schemaConfFileBytes)
	})
)

// compileSchema compiles one embedded schema, labeling a failure with its kind. A failure
// is a rotini build defect, not a user error.
func compileSchema(kind string, schemaBytes []byte) (*jsonschema.Schema, error) {
	schema, err := jsonschema.Compile(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("compile embedded %s schema: %w", kind, err)
	}
	return schema, nil
}
