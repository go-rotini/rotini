package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoVaultReadHandler)(nil)

type demoVaultReadHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoVaultReadHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoVaultReadInputs](); err != nil {
		return err
	}

	_, err := fmt.Fprintln(rtx.Stdout, "demo vault read")
	return err
}
