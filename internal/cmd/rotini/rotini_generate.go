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
	if answerHelp(rtx, func(in RotiniGenerateInputs) bool { return in.RotiniGenerate.Flags.Help }) {
		return
	}

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

	// The warnings are validate's, plus what the pass removed — generating is not supposed to
	// be destructive, so on the rare occasion it is, it says so — and what the handler-hook
	// audit found in files rotini did not write.
	if err := generate(spec, conf, flags.Watch, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}
