package handlers

import (
	"errors"

	"github.com/go-rotini/rotini/internal"
	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// GenerateHandlerImpl handles `rotini generate <file>` (alias
// `rotini gen`). Validates the spec, then runs the full codegen
// pipeline (framework + bridge + skeletons) atomically. Watch mode
// is reserved for a follow-up.
type GenerateHandlerImpl struct{}

// Run satisfies [rotini.GenerateHandler].
func (h *GenerateHandlerImpl) Run(ctx rotini.GenerateCtx, inputs *rotini.GenerateInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyGenerate))
		return nil
	}

	if inputs.Flags.Watch {
		_, _ = io.Stderr.Println("Error: --watch is not yet implemented")
		return &rtk.EarlyExit{Code: 1}
	}

	specPath := inputs.Arguments.File
	if specPath == "" {
		specPath = ".rotini.spec.yaml"
	}

	res, err := internal.Run(internal.RunOptions{
		SpecPath: specPath,
		ConfPath: inputs.Flags.Config,
	})
	if err != nil {
		// Drain multi-error chains so each validation issue surfaces
		// on its own line — matches rotiniold's UX.
		printMultiError(io.Stderr, err)
		return &rtk.EarlyExit{Code: 1}
	}
	for _, p := range res.FilesWritten {
		_, _ = io.Stdout.Println("wrote:", p)
	}
	for _, p := range res.FilesSkipped {
		_, _ = io.Stdout.Println("kept: ", p)
	}
	return nil
}

// printMultiError flattens a multi-error (the shape SpecError uses,
// where Unwrap returns []error) into one line per issue. Falls back
// to a single-line print for non-multi errors.
func printMultiError(w *rtk.Writer, err error) {
	// errors.AsType[T] only works for T : error; the multi-error
	// interface (Unwrap() []error) doesn't implement Error(), so we
	// fall back to a type-assertion via errors.As on a pointer.
	type multi interface{ Unwrap() []error }
	var m multi
	if errors.As(err, &m) {
		for _, e := range m.Unwrap() {
			_, _ = w.Println(e)
		}
		return
	}
	_, _ = w.Println(err)
}
