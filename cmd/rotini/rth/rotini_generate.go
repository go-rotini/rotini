package rth

import (
	"context"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniGenerateHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	signals := rotini.MustGet[*rtk.Signals](rtx, "signals")
	// Derive a cancellable run context and hand it to Run through the registry
	// (hooks each receive the program ctx, so a child ctx can't be threaded via
	// the ctx arg). SIGINT cancels it; --watch honours it and returns, then
	// teardown runs in CascadingPostRun.
	runCtx, cancel := context.WithCancel(ctx)
	rtx.Bind("runctx", runCtx)
	signals.Add(os.Interrupt, func() { cancel() })
	signals.Start(ctx)
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
	in := inputs.RotiniGenerate

	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniGenerate)
		return
	}

	if in.Flags.Watch {
		runCtx := rotini.MustGet[context.Context](rtx, "runctx")
		if err := internal.GenerateWatch(runCtx, in.Arguments.File, in.Flags.Config, io.Stdout); err != nil {
			io.Stderr.Println("Error:", err)
			rtx.Exit(1)
		}
		return
	}

	if err := internal.Generate(in.Arguments.File, in.Flags.Config); err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

// CascadingPostRun always runs — even when Run calls rtx.Exit — so it is the
// reliable place to stop the signal listener.
func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	rotini.MustGet[*rtk.Signals](rtx, "signals").Stop()
}
