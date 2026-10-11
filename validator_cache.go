package rotini

import (
	"fmt"
	"sync"

	"github.com/go-rotini/recon"
)

// Compiled schemas, keyed by their text, so each compiles once per process. The text never
// changes for a given key, so the cache is safe across runs and goroutines.
var schemaValidators sync.Map // schema text → *recon.JSONSchemaValidator

// schemaValidator returns the compiled validator for a JSON Schema.
func schemaValidator(schema string) (*recon.JSONSchemaValidator, error) {
	if cached, ok := schemaValidators.Load(schema); ok {
		if v, ok := cached.(*recon.JSONSchemaValidator); ok {
			return v, nil
		}
	}
	v, err := recon.NewJSONSchemaValidator([]byte(schema))
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	schemaValidators.Store(schema, v)
	return v, nil
}
