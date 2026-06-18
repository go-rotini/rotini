//go:generate go tool jsonschema generate -package internal -root Spec -o ../../internal/schema_spec.go ../../internal/schema-spec.json
//go:generate go tool jsonschema generate -package internal -root Conf -o ../../internal/schema_conf.go ../../internal/schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"github.com/go-rotini/rotini"
	r "github.com/go-rotini/rotini/internal/cmd/rotini"
)

var (
	version = "0.0.0"
)

func main() {
	r.Program.
		Bind(rotini.KeyParser, rotini.NewParser()).
		Bind(rotini.KeySuggestor, rotini.NewSuggestor()).
		Bind(rotini.KeyVersioner, rotini.NewVersioner(version)).
		Execute()
}
