package rth

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
)

type rotiniHandlers struct{}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.Context) {
	// parser, err := rtx.Get[*rotini.Parser]("parser") // this gets the parser from the "internal service registry" that the rotini context is aware of/holds.
	// parser := rtx.MustGet[*rotini.Parser]("parser") // this gets the parser from the "internal service registry" that the rotini context is aware of/holds. The difference between "Get" and "MustGet" is that "MustGet" will panic.
	// inputs, err := parser.Parse[rtg.RotiniInputs](rtx) // this works similarly to the existing "rotini.Inputs[rtg.RotiniInputs](rtx)" function. A big difference is how behavior/control is give to the end-user. Currently a "bad input" for a command -- i.e. "rotini --badflag" will be caught "internally" to the rotini cli framework and handled like:
	/*
		❯ rotini --badflag
		rotini: unknown flag "--badflag"
		Run 'rotini --help' for usage.
	*/
	// But this takes "control" away from the end-user. This change suggests the "go idiomatic" way of returning the err and letting the user handle from where it was called is a better way for inversion of control.
	// This also makes "rotini behaviors" much more opt-in. If a user wants to use a completely different flag parser, they should be able to do so by getting at the raw argv args from rtx.Args. In this way, rotini cli framework functionality will become much more composable. User's opt-in or opt-out of what they want, with a few "core" things being the line of "if you disagree with these parts of the rotini cli framework, then you might not want to use the rotini cli framework". Right now I think that can be effectively be trimmed down to how rotini codegens the command handler files (like this one) and the logic the rotini cli framework uses to "figure out" which command was issued to run the correct handler tree of CascadingPreRun/CascadingPostRun methods and the specific command PreRun/Run/PostRun methods.

	flags := rotini.Inputs[rtg.RotiniInputs](rtx).Rotini.Flags

	switch {
	case flags.Help:
		fmt.Println(helpTextRotini)
	case flags.Version:
		fmt.Println(rtg.Version)
	default:
		fmt.Println(helpTextRotini)
		rotini.Exit(rtx, 1)
	}
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.Context) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.Context) {
}
