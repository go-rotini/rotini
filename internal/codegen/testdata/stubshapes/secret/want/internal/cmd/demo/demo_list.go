package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoListHandler)(nil)

type demoListHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo list", inputs); err != nil {
		rtx.HaltWith(err)
	}
}
