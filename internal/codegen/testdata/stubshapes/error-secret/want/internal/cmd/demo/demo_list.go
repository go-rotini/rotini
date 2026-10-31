package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoListHandler)(nil)

type demoListHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoListHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoListInputs]()
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo list", inputs)
	return err
}
