package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniGenerateHandler)(nil)

type rotiniGenerateHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniGenerateHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniGenerateInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	spec, conf, ok := resolveInputs(rtx, args.SpecFilePath, flags.ConfFilePath)
	if !ok {
		return
	}

	version := rtx.Version()
	rtx.SetDependencyIfAbsent(generateDep, codegen.NewProcessor(version).Generate)
	generate := rtx.MustGetDependency(generateDep)

	// Warnings are validate's, plus each pruned file and the handler audit's findings.
	if err := generate(spec, conf, flags.Watch, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}
