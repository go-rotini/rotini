//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "example.com/demo/internal/cmd/demo"
)

// go build -ldflags "-X main.version=1.2.3" ./cmd/...
var version = "0.0.0"

func main() {
	cmd.NewProgram(cmd.Handlers()).
		WithVersion(version).
		Execute()
}
