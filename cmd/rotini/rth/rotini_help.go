package rth

import (
	"context"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHelpHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniHelpInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniHelp
	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniHelp)
		return
	}

	// rtg.Help resolves the command path (names or aliases; no args = root) to
	// its generated help text.
	text, err := rtg.Help(in.Arguments.Command...)
	if err != nil {
		io.Stderr.Printf("Error: %v\n\n", err)
		io.Stdout.Println(rtg.HelpRotini)
		rtx.Exit(1)
		return
	}
	io.Stdout.Println(text)
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
