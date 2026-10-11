// Package main is an acceptance-tier fixture: a program built with NewWiredProgram that
// declares a plugin but does not wire the plugins feature.
package main

import (
	"context"

	"github.com/go-rotini/rotini"
)

type root struct{ rotini.NoHooks }

func (root) Run(context.Context, *rotini.Context) {}

func main() {
	def := rotini.Definition{
		Name:    "wired",
		Handler: "Wired",
		Plugins: []rotini.PluginDef{{Name: "ctx", Binary: "wired-ctx"}},
	}
	_ = rotini.NewWiredProgram(def, func(string) (rotini.Handler, bool) { return root{}, true }).Execute()
}
