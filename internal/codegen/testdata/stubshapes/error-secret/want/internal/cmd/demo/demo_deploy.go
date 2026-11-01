package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoDeployHandler)(nil)

type demoDeployHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoDeployHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoDeployInputs](); err != nil {
		return err
	}

	_, err := fmt.Fprintln(rtx.Stdout, "demo deploy")
	return err
}
