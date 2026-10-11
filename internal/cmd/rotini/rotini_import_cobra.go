package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniImportCobraHandler)(nil)

type rotiniImportCobraHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniImportCobraHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniImportCobraInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniImportCobra.Arguments
	flags := inputs.RotiniImportCobra.Flags
	runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkCobra,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Strict:    flags.Strict,
	}, flags.RotiniImportFlagSet)
}
