package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniVersionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniVersionHandlers)(nil)

func (*rotiniVersionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser, ok := rtx.Get("parser").(*rtk.Parser)
	if !ok {
		fmt.Fprintln(os.Stderr, "rotini: parser not bound")
		rtx.Exit(1)
		return
	}

	var inputs rtg.RotiniVersionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	if inputs.RotiniVersion.Flags.Help {
		fmt.Println(helpTextRotiniVersion)
		return
	}

	fmt.Println(rtg.Version)
}

func (*rotiniVersionHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniVersionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
