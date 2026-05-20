package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniVersionHandlers struct{}

var _ rotini.RotiniVersionHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniVersionCtx) {}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rotini.RotiniVersionCtx) {}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rotini.RotiniVersionCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	if rtx.Inputs.RotiniVersion.Flags.Help {
		helpString := getRotiniHelp(helpKeyRotiniVersion)
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	io.Stdout.Println(rotini.RotiniDefinition.Metadata.Version)
	os.Exit(0)
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rotini.RotiniVersionCtx) {}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniVersionCtx) {}
