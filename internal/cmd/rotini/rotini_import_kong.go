package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniImportKongHandler)(nil)

type rotiniImportKongHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniImportKongHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniImportKongInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniImportKong.Arguments
	flags := inputs.RotiniImportKong.Flags
	return runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkKong,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Vars:      flags.Var,
	}, flags.RotiniImportFlagSet)
}
