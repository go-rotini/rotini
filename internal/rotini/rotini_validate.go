package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal"
	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniValidateHandlers struct{}

var _ rotini.RotiniValidateHandlers = (*rotiniValidateHandlers)(nil)

func (*rotiniValidateHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniValidateCtx) {}

func (*rotiniValidateHandlers) PreRun(ctx context.Context, rtx rotini.RotiniValidateCtx) {}

func (*rotiniValidateHandlers) Run(ctx context.Context, rtx rotini.RotiniValidateCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	if rtx.Inputs.RotiniValidate.Flags.Help {
		helpString := getRotiniHelp(helpKeyRotiniValidate)
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	if _, err := internal.Validate(rtx.Inputs.RotiniValidate.Arguments.File, ""); err != nil {
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

func (*rotiniValidateHandlers) PostRun(ctx context.Context, rtx rotini.RotiniValidateCtx) {}

func (*rotiniValidateHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniValidateCtx) {}
