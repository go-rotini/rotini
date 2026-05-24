package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser := rtk.MustGet[*rtk.Parser](rtx, "parser")
	io := rtk.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("Error:", err)
		io.Stdout.Println(helpTextRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		io.Stdout.Println(helpTextRotini)
	case flags.Version:
		io.Stdout.Println(rtg.Version)
	default:
		io.Stdout.Println(helpTextRotini)
		rtx.Exit(1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
