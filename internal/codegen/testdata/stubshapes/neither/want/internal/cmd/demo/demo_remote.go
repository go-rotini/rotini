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
	fmt.Fprintln(rtx.Stdout, rtx.Help())
	rtx.HaltWithCode(1)
}
