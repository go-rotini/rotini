package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoHandler)(nil)

type demoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	argv, err := rtx.ArgvInputs[DemoInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	inputs := argv.Values

	if inputs.Demo.Flags.Version {
		version := rtx.Version()
		if version == "" {
			version = "unknown"
		}

		if _, err := fmt.Fprintln(rtx.Stdout, version); err != nil {
			rtx.HaltWith(err)
			return
		}

		rtx.HaltWithCode(0)
		return
	}
}

func (*demoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[DemoInputs](); err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintln(rtx.Stdout, rtx.Help()); err != nil {
		rtx.HaltWith(err)
		return
	}

	rtx.HaltWithCode(1)
}
