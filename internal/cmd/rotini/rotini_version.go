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
	inputs, err := rotini.Collect[RotiniVersionInputs](rtx)
	if err != nil {
		fmt.Fprintln(rtx.Stderr, err)
		rtx.SignalExit(1)
		return
	}

	flags := inputs.RotiniVersion.Flags
	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniVersion)
		rtx.SignalExit(0)
		return
	}

	version := rtx.MustGet[string](KeyRotiniVersion)
	fmt.Fprintf(rtx.Stdout, "v%s\n", version)
	rtx.SignalExit(0)
}
