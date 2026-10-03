package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*demoHandlers)(nil)

type demoHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*demoHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[DemoInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	fmt.Fprintf(rtx.Stdout, "%s: %+v\n", "demo", inputs)
}
