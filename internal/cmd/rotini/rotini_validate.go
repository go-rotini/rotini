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
	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")

	var inputs RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotiniValidate)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniValidate)
		rtx.Exit(0)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	version := rotini.MustGet[string](rtx, "version")
	rtx.BindIfAbsent("validate", internal.NewProcessor(version).Validate)
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
	)

	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
	}

	rtx.Exit(0)
}
