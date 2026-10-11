package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoDeployHandler)(nil)

type demoDeployHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoDeployHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[DemoDeployInputs](); err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintln(rtx.Stdout, "demo deploy"); err != nil {
		rtx.HaltWith(err)
	}
}
