//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"github.com/go-rotini/rotini"
	cli "github.com/go-rotini/rotini/examples/acme/internal/acme"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.Program.
		Bind(rotini.KeyParser, rotini.NewParser()).
		Bind(rotini.KeySuggestor, rotini.NewSuggestor()).
		Bind(rotini.KeyVersioner, rotini.NewVersioner(version)).
		Bind(rotini.KeyCommit, commit).
		Bind(rotini.KeyDate, date).
		Execute()
}
