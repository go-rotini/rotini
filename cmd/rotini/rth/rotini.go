package rth

import (
	"context"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser, ok := rtx.Get("parser").(*rtk.Parser)
	if !ok {
		fmt.Fprintln(os.Stderr, "Error: parser not bound")
		rtx.Exit(1)
		return
	}

	var inputs rtg.RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Println(helpTextRotini)
		rtx.Exit(1)
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		fmt.Println(helpTextRotini)
	case flags.Version:
		fmt.Println(rtg.Version)
	default:
		fmt.Println(helpTextRotini)
		rtx.Exit(1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
