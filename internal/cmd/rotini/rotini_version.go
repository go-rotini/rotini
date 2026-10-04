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
	if answerHelp(rtx, func(in RotiniVersionInputs) bool { return in.RotiniVersion.Flags.Help }) {
		return
	}

	if _, err := rtx.Inputs[RotiniVersionInputs](); err != nil {
		haltWithInputError(rtx, err)
		return
	}

	fmt.Fprintf(rtx.Stdout, "v%s\n", rtx.Version())
	rtx.HaltWithCode(0)
}
