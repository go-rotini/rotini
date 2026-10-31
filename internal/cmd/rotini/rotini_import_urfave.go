package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniImportUrfaveHandler)(nil)

type rotiniImportUrfaveHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniImportUrfaveHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniImportUrfaveInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniImportUrfave.Arguments
	flags := inputs.RotiniImportUrfave.Flags
	return runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkURFave,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Strict:    flags.Strict,
	}, flags.RotiniImportFlagSet)
}
