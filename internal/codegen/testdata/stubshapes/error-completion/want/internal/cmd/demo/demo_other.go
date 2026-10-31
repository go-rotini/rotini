package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoOtherHandler)(nil)

type demoOtherHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoOtherHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoOtherInputs](); err != nil {
		return err
	}

	fmt.Fprintln(rtx.Stderr, rtx.Help())
	return rotini.MissingCommand(rtx)
}
