package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoRemoteAddHandler)(nil)

type demoRemoteAddHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoRemoteAddHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoRemoteAddInputs]()
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo remote add", inputs)
	return err
}
