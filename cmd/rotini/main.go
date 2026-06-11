//go:generate go tool gentypes -package internal -root Spec -o ../../internal/schema_spec_types.go ../../internal/schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/schema_conf_types.go ../../internal/schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"github.com/go-rotini/rotini"
	r "github.com/go-rotini/rotini/internal/cmd/rotini"
)

var (
	version = "0.0.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	r.Program.
		Bind("parser", rotini.NewParser()).
		Bind("build", rotini.BuildInfo(version)).
		Bind("commit", commit).
		Bind("date", date).
		Execute()
}
