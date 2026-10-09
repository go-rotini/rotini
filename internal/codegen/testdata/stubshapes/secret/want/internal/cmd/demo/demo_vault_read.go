package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoVaultReadHandler)(nil)

type demoVaultReadHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoVaultReadHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[DemoVaultReadInputs](); err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintln(rtx.Stdout, "demo vault read"); err != nil {
		rtx.HaltWith(err)
	}
}
