package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("Error:", err)
		io.Stdout.Println(rtg.HelpRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		io.Stdout.Println(rtg.HelpRotini)
	case flags.Version:
		io.Stdout.Println(rtg.Version)
	default:
		io.Stdout.Println(rtg.HelpRotini)
		rtx.Exit(1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Println("rotini PostRun")
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	fmt.Println("rotini CascadingPostRun")
}
