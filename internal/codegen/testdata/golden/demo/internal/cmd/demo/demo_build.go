package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoBuildHandlers)(nil)

type demoBuildHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*demoBuildHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[DemoBuildInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo build", inputs)
}
