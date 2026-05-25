package rth

import (
	"context"
	"os"
	"path/filepath"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniCompletionHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniCompletionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(2)
		return
	}
	in := inputs.RotiniCompletion

	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniCompletion)
		return
	}

	script, err := rtk.CompletionScript(filepath.Base(os.Args[0]), in.Arguments.Shell)
	if err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
	io.Stdout.Print(script)
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx *rotini.Context) {
}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
}
