package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoHandler)(nil)

type demoHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rtx.ArgvInputs[DemoInputs](); err == nil {
		if argv.Values.Demo.Flags.Help {
			if _, err := fmt.Fprintln(rtx.Stdout, rtx.Help()); err != nil {
				rtx.HaltWith(err)
				return
			}

			rtx.HaltWithCode(0)
			return
		}

		if argv.Values.Demo.Flags.Version {
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
