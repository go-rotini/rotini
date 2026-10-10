package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoOtherCompletionHandler)(nil)

type demoOtherCompletionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoOtherCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoOtherCompletionInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo other completion", inputs); err != nil {
		rtx.HaltWith(err)
	}
}
