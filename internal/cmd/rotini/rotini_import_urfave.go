package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniImportUrfaveHandler)(nil)

type rotiniImportUrfaveHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniImportUrfaveHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniImportUrfaveInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniImportUrfave.Arguments
	flags := inputs.RotiniImportUrfave.Flags
	runImport(ctx, rtx, codegen.ImportOptions{
		Framework: codegen.FrameworkURFave,
		Package:   args.Package,
		Root:      flags.Root,
		Name:      flags.Name,
		Strict:    flags.Strict,
	}, flags.RotiniImportFlagSet)
}
