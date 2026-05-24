package rth

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniHelpHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

// helpKeyForPath maps a command path (e.g. "generate" or its alias "gen") to its
// help key. The second return is false for an unrecognized path.
func helpTextForPath(path string) (string, bool) {
	switch path {
	case "":
		return helpTextRotini, true
	case "initialize", "init":
		return helpTextRotiniInitialize, true
	case "generate", "gen":
		return helpTextRotiniGenerate, true
	case "validate", "val":
		return helpTextRotiniValidate, true
	case "completion":
		return helpTextRotiniCompletion, true
	case "version":
		return helpTextRotiniVersion, true
	case "help":
		return helpTextRotiniHelp, true
	default:
		return helpTextRotini, false
	}
}

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx rotini.Context) {
	parser, err := rotini.Get[*rtk.Parser](rtx, rtk.ParserKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}

	inputs, err := rtk.Parse[rtg.RotiniHelpInputs](parser, rtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniHelp
	if in.Flags.Help {
		fmt.Println(helpTextRotiniHelp)
		return
	}

	path := strings.Join(in.Arguments.Command, " ")
	text, ok := helpTextForPath(path)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\n", path)
		fmt.Println(helpTextRotiniHelp)
		rtx.Exit(1)
		return
	}
	fmt.Println(text)
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
