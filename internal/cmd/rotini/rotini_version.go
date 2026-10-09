package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniVersionHandler)(nil)

type rotiniVersionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniVersionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[RotiniVersionInputs](); err != nil {
		haltWithInputError(rtx, err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "v%s\n", rtx.Version()); err != nil {
		haltWithWriteError(rtx, err)
		return
	}
	rtx.HaltWithCode(0)
}
