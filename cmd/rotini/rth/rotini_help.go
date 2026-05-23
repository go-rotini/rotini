package rth

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniHelpHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHelpHandlers)(nil)

// helpKeyForPath maps a command path (e.g. "generate" or its alias "gen") to its
// help key. The second return is false for an unrecognized path.
func helpKeyForPath(path string) (helpKey, bool) {
	switch path {
	case "":
		return helpKeyRotini, true
	case "initialize", "init":
		return helpKeyRotiniInitialize, true
	case "generate", "gen":
		return helpKeyRotiniGenerate, true
	case "validate", "val":
		return helpKeyRotiniValidate, true
	case "completion":
		return helpKeyRotiniCompletion, true
	case "version":
		return helpKeyRotiniVersion, true
	case "help":
		return helpKeyRotiniHelp, true
	default:
		return helpKeyRotini, false
	}
}

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx rotini.Context) {
	in := rotini.Inputs[rtg.RotiniHelpInputs](rtx).RotiniHelp
	if in.Flags.Help {
		fmt.Println(getRotiniHelp(helpKeyRotiniHelp))
		return
	}

	path := strings.Join(in.Arguments.Command, " ")
	key, ok := helpKeyForPath(path)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\n", path)
		fmt.Println(getRotiniHelp(helpKeyRotini))
		rotini.Exit(rtx, 1)
		return
	}
	fmt.Println(getRotiniHelp(key))
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
