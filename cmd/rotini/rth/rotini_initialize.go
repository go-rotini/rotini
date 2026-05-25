package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniInitializeHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniInitializeHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniInitialize

	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniInitialize)
		return
	}

	if err := internal.Initialize(in.Arguments.Name, in.Flags.Format, in.Flags.Force, in.Flags.Into); err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniInitializeHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniInitializeHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
