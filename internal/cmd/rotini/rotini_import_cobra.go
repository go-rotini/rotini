package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniImportCobraHandler)(nil)

type rotiniImportCobraHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniImportCobraHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniImportCobraInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniImportCobra.Arguments
	flags := inputs.RotiniImportCobra.Flags
	return runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkCobra,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Strict:    flags.Strict,
	}, flags.RotiniImportFlagSet)
}
