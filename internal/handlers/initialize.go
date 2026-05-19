package handlers

import (
	"fmt"

	"github.com/go-rotini/rotini/internal"
	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// InitializeHandlerImpl handles `rotini initialize <name>` (alias
// `rotini init`). Lays down a fresh .rotini.spec.yaml,
// .rotini.conf.yaml, and main.go in the current directory.
type InitializeHandlerImpl struct{}

// Run satisfies [rotini.InitializeHandler].
func (h *InitializeHandlerImpl) Run(ctx rotini.InitializeCtx, inputs *rotini.InitializeInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyInitialize))
		return nil
	}

	if inputs.Arguments.Name == "" {
		_, _ = io.Stderr.Println("Error: name argument is required")
		fprintln(io.Stdout, getHelp(helpKeyInitialize))
		return &rtk.EarlyExit{Code: 1}
	}

	_, err := internal.Initialize(internal.InitOptions{
		Name:          inputs.Arguments.Name,
		Force:         inputs.Flags.Force,
		RotiniVersion: Version,
	})
	if err != nil {
		_, _ = io.Stderr.Println(fmt.Sprintf("Error: %s", err.Error()))
		fprintln(io.Stdout, getHelp(helpKeyInitialize))
		return &rtk.EarlyExit{Code: 1}
	}
	return nil
}
