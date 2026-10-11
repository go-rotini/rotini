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
	if _, err := fmt.Fprintln(rtx.Stdout, rtx.Help()); err != nil {
		rtx.HaltWith(err)
		return
	}

	rtx.HaltWithCode(1)
}
