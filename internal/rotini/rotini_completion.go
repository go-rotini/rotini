package cmd

import (
	"context"

	"github.com/matthewgetz/rotini/internal/rotini"
	rr "github.com/matthewgetz/rotini/runtime"
	rt "github.com/matthewgetz/rotini/toolkit"
)

type rotiniCompletionHandlers struct{}

var _ rotini.RotiniCompletionHandlers = (*rotiniCompletionHandlers)(nil)

func (*rotiniCompletionHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniCompletionCtx) {
}

func (*rotiniCompletionHandlers) PreRun(ctx context.Context, rtx rotini.RotiniCompletionCtx) {}

func (*rotiniCompletionHandlers) Run(ctx context.Context, rtx rotini.RotiniCompletionCtx) {
	io := rr.GetRegisteredService[*rt.RotiniIO](rtx.Services, "io")
	os := rr.GetRegisteredService[*rr.RotiniOS](rtx.Services, "os")

	if rtx.Inputs.RotiniCompletion.Flags.Help {
		helpString := getRotiniHelp(helpKeyRotiniCompletion)
		io.Stdout.Println(helpString)
		os.Exit(0)
	}

	def := rr.CompletionDefinition{
		Name:           rotini.RotiniDefinition.Name,
		Commands:       rotini.RotiniDefinition.Commands,
		Flags:          rotini.RotiniDefinition.Flags,
		RemoteCommands: rotini.RotiniDefinition.RemoteCommands,
	}

	var output string
	switch rtx.Inputs.RotiniCompletion.Arguments.Shell {
	case "bash":
		output = rr.GenerateBashCompletion(def)
	case "zsh":
		output = rr.GenerateZshCompletion(def)
	case "fish":
		output = rr.GenerateFishCompletion(def)
	case "powershell":
		output = rr.GeneratePowershellCompletion(def)
	case "nushell":
		output = rr.GenerateNushellCompletion(def)
	case "elvish":
		output = rr.GenerateElvishCompletion(def)
	default:
		io.Stderr.Println("Error: shell argument is required (zsh, bash, fish, powershell, nushell, elvish)")
		os.Exit(1)
	}

	io.Stdout.Print(output)
}

func (*rotiniCompletionHandlers) PostRun(ctx context.Context, rtx rotini.RotiniCompletionCtx) {}

func (*rotiniCompletionHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniCompletionCtx) {
}
