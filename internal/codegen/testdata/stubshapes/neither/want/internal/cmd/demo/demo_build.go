package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoBuildHandler)(nil)

type demoBuildHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoBuildHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoBuildInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs); err != nil {
		rtx.HaltWith(err)
	}
}
