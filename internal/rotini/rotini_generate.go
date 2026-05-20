package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal"
	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniGenerateHandlers struct{}

var _ rotini.RotiniGenerateHandlers = (*rotiniGenerateHandlers)(nil)

func (*rotiniGenerateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniGenerateCtx) {}

func (*rotiniGenerateHandlers) PreRun(ctx context.Context, rtx rotini.RotiniGenerateCtx) {}

func (*rotiniGenerateHandlers) Run(ctx context.Context, rtx rotini.RotiniGenerateCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	if rtx.Inputs.RotiniGenerate.Flags.Help {
		helpString := getRotiniHelp(helpKeyRotiniGenerate)
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	configFile := rtx.Inputs.RotiniGenerate.Flags.Config

	if _, err := internal.Validate(rtx.Inputs.RotiniGenerate.Arguments.File, configFile); err != nil {
		type multiErr interface{ Unwrap() []error }
		if me, ok := err.(multiErr); ok {
			for _, e := range me.Unwrap() {
				io.Stderr.Println(e)
			}
		} else {
			io.Stderr.Println(err)
		}
		os.Exit(1)
	}

	if _, err := internal.Generate(ctx, rtx.Inputs.RotiniGenerate.Arguments.File, configFile, rtx.Inputs.RotiniGenerate.Flags.Watch); err != nil {
		type multiErr interface{ Unwrap() []error }
		if me, ok := err.(multiErr); ok {
			for _, e := range me.Unwrap() {
				io.Stderr.Println(e)
			}
		} else {
			io.Stderr.Println(err)
		}
		os.Exit(1)
	}
}

func (*rotiniGenerateHandlers) PostRun(ctx context.Context, rtx rotini.RotiniGenerateCtx) {}

func (*rotiniGenerateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniGenerateCtx) {}
