package handlers

import (
	"github.com/go-rotini/rotini/internal"
	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// ValidateHandlerImpl handles `rotini validate <file>` (alias
// `rotini val`). Loads + validates the spec; prints every issue
// found and exits non-zero on failure.
type ValidateHandlerImpl struct{}

// Run satisfies [rotini.ValidateHandler].
func (h *ValidateHandlerImpl) Run(ctx rotini.ValidateCtx, inputs *rotini.ValidateInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyValidate))
		return nil
	}

	specPath := inputs.Arguments.File
	if specPath == "" {
		specPath = ".rotini.spec.yaml"
	}

	spec, err := internal.LoadSpec(specPath)
	if err != nil {
		_, _ = io.Stderr.Println(err)
		return &rtk.EarlyExit{Code: 1}
	}
	if err := internal.Validate(spec); err != nil {
		printMultiError(io.Stderr, err)
		return &rtk.EarlyExit{Code: 1}
	}
	_, _ = io.Stdout.Println("OK")
	return nil
}
