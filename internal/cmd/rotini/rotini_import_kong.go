package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniImportKongHandler)(nil)

type rotiniImportKongHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniImportKongHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniImportKongInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniImportKong.Arguments
	flags := inputs.RotiniImportKong.Flags
	runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkKong,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Vars:      flags.Var,
	}, flags.RotiniImportFlagSet)
}
