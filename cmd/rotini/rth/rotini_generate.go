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

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniGenerateInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}

	args := inputs.RotiniGenerate.Arguments
	flags := inputs.RotiniGenerate.Flags

	switch {
	case flags.Help:
		io.Stdout.Println(rtg.HelpRotiniGenerate)
	case flags.Watch:
		if err := internal.GenerateWatch(args.SpecFilePath, flags.ConfFilePath, io.Stdout); err != nil {
			io.Stderr.Println("Error:", err)
			rtx.Exit(1)
		}
	default:
		if err := internal.Generate(args.SpecFilePath, flags.ConfFilePath); err != nil {
			io.Stderr.Println("Error:", err)
			rtx.Exit(1)
		}
	}
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
