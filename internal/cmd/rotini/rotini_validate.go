package rotini

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.ErrorHandler = (*rotiniValidateHandler)(nil)

type rotiniValidateHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniValidateHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniValidateInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags
	if flags.Format == RotiniValidateFormatJSON {
		return validateJSON(rtx, args.SpecFilePath, flags)
	}

	spec, conf, err := resolveInputs(rtx, args.SpecFilePath, flags.ConfFilePath)
	if err != nil {
		return err
	}

	release, err := releaseOf(rtx, spec, conf, flags.Release)
	if err != nil {
		return rotini.UsageError(err)
	}

	version := rtx.Version()
	rtx.SetDependencyIfAbsent(validateDep, codegen.NewProcessor(version).Validate)
	validate := rtx.MustGetDependency(validateDep)

	// Warnings never fail the run.
	if err := validate(spec, conf, flags.Watch, flags.Fail, release, printResult(rtx), printWarnings(rtx)); err != nil {
		return problems(rtx, err)
	}

	rtx.HaltWithCode(0)
	return nil
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
