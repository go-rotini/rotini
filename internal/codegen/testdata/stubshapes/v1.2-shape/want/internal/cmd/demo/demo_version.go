package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoVersionHandler)(nil)

type demoVersionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoVersionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rtx.ArgvInputs[DemoVersionInputs](); err == nil && argv.Values.Demo.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	if _, err := rtx.Inputs[DemoVersionInputs](); err != nil {
		rtx.HaltWith(err)
		return
	}

	version := rtx.Version()
	if version == "" {
		version = "unknown"
	}

	fmt.Fprintln(rtx.Stdout, version)
}
