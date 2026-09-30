package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*rotiniVersionHandlers)(nil)

type rotiniVersionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniVersionInputs) bool { return in.RotiniVersion.Flags.Help }) {
		return
	}

	if _, err := rotini.Collect[RotiniVersionInputs](rtx); err != nil {
		haltWithInputError(rtx, err)
		return
	}

	fmt.Fprintf(rtx.Stdout, "v%s\n", rtx.Version())
	rtx.HaltWithCode(0)
}
