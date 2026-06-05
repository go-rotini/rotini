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
		fmt.Fprintln(rtx.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniValidate

	if in.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtg.HelpRotiniValidate)
		return
	}

	if err := internal.Validate(in.Arguments.File, "", in.Flags.Fail); err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.Exit(1)
		return
	}
}
