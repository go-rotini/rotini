//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"github.com/go-rotini/rotini"

	cmd "example.com/demo/internal/cmd/demo"
)

// version is what `--version` reports. Stamp it at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/...
var version = "0.0.0"

func main() {
	cmd.Program.
		Bind(rotini.KeyVersion, version).
		Execute()
}
