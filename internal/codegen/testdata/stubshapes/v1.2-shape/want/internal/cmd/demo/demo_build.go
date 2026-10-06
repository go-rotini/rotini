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
	if argv, err := rtx.ArgvInputs[DemoBuildInputs](); err == nil && argv.Values.Demo.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	inputs, err := rtx.Inputs[DemoBuildInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs)
}
