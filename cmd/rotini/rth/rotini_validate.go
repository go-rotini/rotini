package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniValidateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniValidateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniValidate

	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniValidate)
		return
	}

	if err := internal.Validate(in.Arguments.File, ""); err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
