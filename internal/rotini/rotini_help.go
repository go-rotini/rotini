package rotini

import (
	"context"
	"fmt"
	"strings"

	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniHelpHandlers struct{}

var _ rotini.RotiniHelpHandlers = (*rotiniHelpHandlers)(nil)

func (*rotiniHelpHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniHelpCtx) {}

func (*rotiniHelpHandlers) PreRun(ctx context.Context, rtx rotini.RotiniHelpCtx) {}

func (*rotiniHelpHandlers) Run(ctx context.Context, rtx rotini.RotiniHelpCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	if rtx.Inputs.RotiniHelp.Flags.Help {
		helpString := getRotiniHelp(helpKeyRotiniHelp)
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	commandPathString := strings.Join(rtx.Inputs.RotiniHelp.Arguments.Command, " ")

	var key helpKey
	var err error

	switch commandPathString {
	case "":
		key = helpKeyRotini
	case "generate", "gen":
		key = helpKeyRotiniGenerate
	case "initialize", "init":
		key = helpKeyRotiniInitialize
	case "validate", "val":
		key = helpKeyRotiniValidate
	case "version":
		key = helpKeyRotiniVersion
	case "completion":
		key = helpKeyRotiniCompletion
	case "help":
		key = helpKeyRotiniHelp
	default:
		key = helpKeyRotini
		err = fmt.Errorf("unknown command path \"%s\"", commandPathString)
	}

	helpString := getRotiniHelp(key)

	if err != nil {
		io.Stderr.Printf("Error: %s\n\n", err.Error())
		io.Stdout.Println(helpString)
		os.Exit(1)
	}

	io.Stdout.Println(helpString)
	os.Exit(0)
}

func (*rotiniHelpHandlers) PostRun(ctx context.Context, rtx rotini.RotiniHelpCtx) {}

func (*rotiniHelpHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniHelpCtx) {}
