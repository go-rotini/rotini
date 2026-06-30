//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "example.com/demo/internal/cmd/demo"
)

func main() {
	cmd.Program.Execute()
}
