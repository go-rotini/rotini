//go:generate go tool gentypes -package internal -root Spec -o ../../internal/spec_types.go ../../internal/schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/conf_types.go ../../internal/schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"regexp"
	"runtime/debug"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/cli"
)

func version() string {
	verFallback := "v0.0.0"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return verFallback
	}
	releaseTagRe := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	if ver := info.Main.Version; releaseTagRe.MatchString(ver) {
		return ver
	}
	return verFallback
}

func main() {
	cli.Program.
		Bind("parser", rotini.NewParser()).
		Bind("version", version()).
		Execute()
}
