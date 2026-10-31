package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoOtherCompletionHandler)(nil)

type demoOtherCompletionHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoOtherCompletionHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoOtherCompletionInputs]()
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo other completion", inputs)
	return err
}
