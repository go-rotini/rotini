package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handlers = (*rotiniValidateHandlers)(nil)

type rotiniValidateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniValidateInputs) bool { return in.RotiniValidate.Flags.Help }) {
		return
	}

	inputs, err := rotini.Collect[RotiniValidateInputs](rtx)
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
	rtx.BindIfAbsent("validate", codegen.NewProcessor(version).Validate)
	validate := rtx.MustGet[codegen.ValidateFn]("validate")

	// Warnings never fail the run; they print as each pass reports them.
	if err := validate(spec, conf, flags.Watch, flags.Fail, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}
