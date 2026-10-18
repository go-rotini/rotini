package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoRemoteHandler)(nil)

type demoRemoteHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoRemoteHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[DemoRemoteInputs](); err != nil {
		rtx.HaltWith(err)
		return
	}

	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
