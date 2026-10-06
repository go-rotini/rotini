package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoHelpHandler)(nil)

type demoHelpHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoHelpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rtx.ArgvInputs[DemoHelpInputs](); err == nil && argv.Values.Demo.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	inputs, err := rtx.Inputs[DemoHelpInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	page, err := Help(inputs.DemoHelp.Arguments.Command...)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	fmt.Fprintln(rtx.Stdout, page)
}
