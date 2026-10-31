package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.ErrorHandler = (*demoRemoteHandler)(nil)

type demoRemoteHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoRemoteHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoRemoteInputs](); err != nil {
		return err
	}

	fmt.Fprintln(rtx.Stderr, rtx.Help())
	return rotini.MissingCommand(rtx)
}
