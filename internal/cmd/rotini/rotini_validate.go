package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
)

type rotiniValidateHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniValidate)
		rtx.SignalExit(0)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	rtx.BindIfAbsent("validate", internal.NewProcessor(v.VersionSemantic).Validate)
	validate := rotini.MustGet[internal.ValidateFn](rtx, "validate")

	err := validate(
		args.SpecFilePath,
		flags.ConfFilePath,
		flags.Watch,
		flags.Fail,
		func(result string, err error) {
			if err != nil {
				fmt.Fprintln(rtx.Stderr, "Error:", err)
				return
			}
			fmt.Fprintln(rtx.Stdout, result)
		},
		// Validator warnings are non-fatal: record them so the OnWarning funnel
		// reports them (the default prints "Warning: …"); they never fail the run.
		func(warnings []error) {
			for _, w := range warnings {
				rtx.RecordWarning(w)
			}
		},
	)

	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	rtx.SignalExit(0)
}
