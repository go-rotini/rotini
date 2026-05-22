//go:build ignore

// Command gen is a temporary, build-ignored entrypoint that runs rotini code
// generation by calling internal.Generate directly. It exists so `go generate`
// can regenerate the framework and handler files in place before the rotini
// runtime can self-host — that is, before `rotini generate` actually dispatches
// (program.Execute is still a no-op) and before the generate handler's Run is
// implemented to call internal.Generate itself.
//
// Run via the //go:generate directive in main.go (`go run gen.go`), with the
// working directory at cmd/rotini, so the spec and conf paths below resolve and
// findModule walks up to the module root. Delete this file once the runtime
// dispatch + generate handler land and `go run . generate` self-hosts.
package main

import (
	"fmt"
	"log"

	"github.com/go-rotini/rotini/internal"
)

func main() {
	const (
		specPath = "./.rotini.spec.yaml"     // cmd/rotini/.rotini.spec.yaml
		confPath = "../../.rotini.conf.yaml" // module-root .rotini.conf.yaml
	)
	if err := internal.Generate(specPath, confPath); err != nil {
		log.Fatalf("rotini generate: %v", err)
	}
	fmt.Println("done")
}
