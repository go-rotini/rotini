package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoRemoteAddHandler)(nil)

type demoRemoteAddHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoRemoteAddHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoRemoteAddInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo remote add", inputs); err != nil {
		rtx.HaltWith(err)
	}
}
