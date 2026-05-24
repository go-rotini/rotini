package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniGenerateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser := rtk.MustGet[*rtk.Parser](rtx, "parser")
	io := rtk.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniGenerate

	if in.Flags.Help {
		io.Stdout.Println(helpTextRotiniGenerate)
		return
	}

	if err := internal.Generate(in.Arguments.File, in.Flags.Config); err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
