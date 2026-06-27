package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini/internal/rotini"
)

var _ rotini.Handlers = (*rotiniHandlers)(nil)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniInputs](rtx)
	flags := inputs.Rotini.Flags
	env := inputs.Rotini.Env

	help := HelpRotini
	if flags.Nostyles || env.Nostyles || env.Ci {
		help = rotini.Strip(HelpRotini)
	}

	if err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %s\n\n", err.Error())
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(1)
		return
	}

	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(0)
		return
	case flags.Version:
		v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
		fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
		rtx.SignalExit(0)
		return
	default:
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(1)
		return
	}
}
