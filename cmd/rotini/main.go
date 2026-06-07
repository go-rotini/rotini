//go:generate go tool gentypes -package internal -root Spec -o ../../internal/spec_types.go ../../schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/conf_types.go ../../schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"runtime/debug"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/cli"
)

func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "v0.0.0"
	}
	if ver := bi.Main.Version; ver != "" && ver != "(devel)" {
		return ver
	}
	return "v0.0.0"
}

func main() {
	cli.Program.
		Bind("parser", rotini.NewParser()).
		Bind("version", version()).
		Execute()
}
