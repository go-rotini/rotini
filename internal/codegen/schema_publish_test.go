package codegen

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateSchemas republishes the module-root schema copies:
// `go test ./internal/codegen -run PublishedSchemas -update-schemas`.
var updateSchemas = flag.Bool("update-schemas", false, "rewrite the module-root published schema copies")

// publishedSchemas maps each module-root published file to the embedded bytes it must equal.
func publishedSchemas() map[string][]byte {
	return map[string][]byte{
		"schema-spec.json":     schemaSpecFileBytes,
		"schema-conf.json":     schemaConfFileBytes,
		"schema-contract.json": schemaContractFileBytes,
		"schema-error.json":    errorSchemaBytes,
	}
}

// TestPublishedSchemasInSync keeps the module-root schema copies byte-identical to the
// embedded ones.
//
// The copies exist for ONE reason: a spec's optional `$schema` key points an editor at a
// released schema, and the only URL that resolves without any publishing infrastructure is
// the repository itself at a tag —
//
//	https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/<tag>/schema-spec.json
//
// which serves whatever sits at the module root of that tag. The authoritative files stay
// under internal/codegen (that is what //go:embed compiles in and what validation judges
// against); these are published mirrors, and a mirror that has drifted is worse than no
// mirror at all, because an editor would silently validate against the wrong schema.
func TestPublishedSchemasInSync(t *testing.T) {
	root := filepath.Join("..", "..")
	for name, want := range publishedSchemas() {
		path := filepath.Join(root, name)

		if *updateSchemas {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("published %s", name)
			continue
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("published schema missing (run with -update-schemas): %v", err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s has drifted from internal/codegen/%s — re-run with -update-schemas", name, name)
		}
	}
}
