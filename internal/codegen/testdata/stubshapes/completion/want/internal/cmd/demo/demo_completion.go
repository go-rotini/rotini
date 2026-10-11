package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoCompletionHandler)(nil)

type demoCompletionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoCompletionInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	script, err := Completion(inputs.DemoCompletion.Arguments.Shell)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprint(rtx.Stdout, script); err != nil {
		rtx.HaltWith(err)
	}
}
