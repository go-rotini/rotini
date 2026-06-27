package rotini

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini/internal/codegen"
	"github.com/go-rotini/rotini/internal/rotini"
)

var _ rotini.Handlers = (*rotiniInitializeHandlers)(nil)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniInitialize)
		rtx.SignalExit(0)
		return
	}

	if args.Name == "" {
		rtx.RecordError(rotini.UsageError(errors.New("a name argument is required")))
		rtx.SignalExit(1)
		return
	}

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	rtx.BindIfAbsent("initialize", codegen.NewProcessor(v.VersionSemantic).Initialize)
	initialize := rotini.MustGet[codegen.InitializeFn](rtx, "initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}
}
