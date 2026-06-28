//go:generate go tool jsonschema generate -package codegen -root Spec -o ../../internal/codegen/schema_spec.go ../../internal/codegen/schema-spec.json
//go:generate go tool jsonschema generate -package codegen -root Conf -o ../../internal/codegen/schema_conf.go ../../internal/codegen/schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "github.com/go-rotini/rotini/internal/cmd/rotini"
	"github.com/go-rotini/rotini/internal/rotini"
)

var (
	version = "0.0.0"
)

func main() {
	cmd.Program.
		Bind(cmd.KeyRotiniVersion, cmd.RotiniVersion(version)).
		Bind(rotini.KeyParser, rotini.NewParser()).
		Bind(rotini.KeySuggestor, rotini.NewSuggestor()).
		Execute()
}
