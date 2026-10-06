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
