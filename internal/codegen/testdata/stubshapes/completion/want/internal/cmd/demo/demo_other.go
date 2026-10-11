package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoOtherHandler)(nil)

type demoOtherHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoOtherHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
