//go:generate go tool gentypes -package internal -root Spec -o ../../internal/spec_types.go ../../schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/conf_types.go ../../schema-conf.json
//go:generate go run gen.go
package main

import "github.com/go-rotini/rotini/cmd/rotini/handlers"

func main() {
	handlers.Program.Execute()
}
