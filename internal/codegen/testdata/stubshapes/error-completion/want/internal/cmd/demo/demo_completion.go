package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoCompletionHandler)(nil)

type demoCompletionHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoCompletionInputs]()
	if err != nil {
		return err
	}

	script, err := rtx.CompletionScript(inputs.DemoCompletion.Arguments.Shell)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(rtx.Stdout, script)
	return err
}
