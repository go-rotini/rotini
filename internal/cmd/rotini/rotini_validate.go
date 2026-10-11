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
	inputs, err := rtx.Inputs[RotiniValidateInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags
	if flags.Format == "json" {
		validateJSON(rtx, args.SpecFilePath, flags)
		return
	}

	spec, conf, ok := resolveInputs(rtx, args.SpecFilePath, flags.ConfFilePath)
	if !ok {
		return
	}

	release, err := releaseOf(rtx, spec, conf, flags.Release)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}

	version := rtx.Version()
	rtx.SetDependencyIfAbsent(validateDep, codegen.NewProcessor(version).Validate)
	validate := rtx.MustGetDependency(validateDep)

	// Warnings never fail the run.
	if err := validate(spec, conf, flags.Watch, flags.Fail, release, printResult(rtx), printWarnings(rtx)); err != nil {
		haltWithProblems(rtx, err)
		return
	}

	rtx.HaltWithCode(0)
}

// releaseOf is the release validate checks planned removals against: --release when given,
// else the variable the conf's validate.release_env names, else none. A value that isn't
// X.Y.Z is an error naming where it came from.
func releaseOf(rtx *rotini.Context, spec, conf, flag string) (string, error) {
	if flag != "" {
		return flag, codegen.CheckRelease(flag, "--release")
	}
	name := codegen.ReleaseEnv(spec, conf)
	if name == "" {
		return "", nil
	}
	value, _ := rtx.LookupEnv(name)
	if value == "" {
		return "", nil
	}
	return value, codegen.CheckRelease(value, "$"+name+" (validate.release_env)")
}
