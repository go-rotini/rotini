package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniHandlers struct{}

var _ rotini.RotiniHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniCtx) {}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.RotiniCtx) {}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.RotiniCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")
	h := getRotiniHelp(helpKeyRotini)

	if rtx.Inputs.Rotini.Flags.Help {
		io.Stdout.Println(h)
		os.Exit(0)
	}

	if rtx.Inputs.Rotini.Flags.Version {
		io.Stdout.Println(rotini.RotiniDefinition.Metadata.Version)
		os.Exit(0)
	}

	io.Stdout.Println(h)
	os.Exit(1)
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.RotiniCtx) {}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniCtx) {}
