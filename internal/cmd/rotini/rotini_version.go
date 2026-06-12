package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniVersionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniVersionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n\n", err)
		fmt.Fprintln(rtx.Stdout, HelpRotiniVersion)
		rtx.SignalExit(1)
		return
	}

	flags := inputs.RotiniVersion.Flags
	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniVersion)
		rtx.SignalExit(0)
		return
	}

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
}
