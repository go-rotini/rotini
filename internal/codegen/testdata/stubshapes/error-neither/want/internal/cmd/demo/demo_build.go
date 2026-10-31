package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoBuildHandler)(nil)

type demoBuildHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoBuildHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[DemoBuildInputs]()
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs)
	return err
}
