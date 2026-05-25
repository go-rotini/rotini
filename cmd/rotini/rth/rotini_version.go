package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniVersionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rtk.MustGet[*rtk.Parser](rtx, "parser")
	io := rtk.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniVersionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}

	if inputs.RotiniVersion.Flags.Help {
		io.Stdout.Println(helpTextRotiniVersion)
		return
	}

	io.Stdout.Println(rtg.Version)
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
