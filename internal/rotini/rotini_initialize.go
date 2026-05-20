package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal"
	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniInitializeHandlers struct{}

var _ rotini.RotiniInitializeHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniInitializeCtx) {
}

func (*rotiniInitializeHandlers) PreRun(ctx context.Context, rtx rotini.RotiniInitializeCtx) {}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx rotini.RotiniInitializeCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	args := rtx.Inputs.RotiniInitialize.Arguments
	flags := rtx.Inputs.RotiniInitialize.Flags
	version := rotini.RotiniDefinition.Metadata.Version

	helpString := getRotiniHelp(helpKeyRotiniInitialize)

	if flags.Help {
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	if _, err := internal.Initialize(args.Name, flags.Format, flags.Force, version); err != nil {
		io.Stderr.Printf("Error: %s\n\n", err.Error())
		io.Stdout.Println(helpString)
		os.Exit(1)
	}
}

func (*rotiniInitializeHandlers) PostRun(ctx context.Context, rtx rotini.RotiniInitializeCtx) {}

func (*rotiniInitializeHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniInitializeCtx) {
}
