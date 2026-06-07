package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
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

	var inputs rtg.RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniValidate)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniValidate.Arguments
	flags := inputs.RotiniValidate.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniValidate)
		rtx.Exit(0)
		return
	}

	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n\n", args.SpecFilePath, flags.ConfFilePath)

	rtx.BindIfAbsent("validate", internal.Validate)
	validate := rotini.MustGet[internal.ValidateFn](rtx, "validate")

	if err := validate(args.SpecFilePath, flags.ConfFilePath, flags.Fail); err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}
