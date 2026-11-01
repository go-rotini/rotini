package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoVaultHandler)(nil)

type demoVaultHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoVaultHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoVaultInputs](); err != nil {
		return err
	}

	fmt.Fprintln(rtx.Stderr, rtx.Help())
	return rotini.MissingCommandError(rtx)
}
