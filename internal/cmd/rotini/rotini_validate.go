package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniValidateHandler)(nil)

type rotiniValidateHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniValidateHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniValidateInputs) bool { return in.RotiniValidate.Flags.Help }) {
		return
	}

	inputs, err := rtx.Inputs[RotiniValidateInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags

	spec, conf, ok := resolveInputs(rtx, args.SpecFilePath, flags.ConfFilePath)
	if !ok {
		return
	}

	version := rtx.Version()
	rtx.SetDependencyIfAbsent(validateDep, codegen.NewProcessor(version).Validate)
	validate := rtx.MustGetDependency(validateDep)

	// Warnings never fail the run.
	if err := validate(spec, conf, flags.Watch, flags.Fail, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}
