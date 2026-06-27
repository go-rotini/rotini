package rotini

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini/internal"
	rotini "github.com/go-rotini/rotini/internal/runtime"
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
	rtx.BindIfAbsent("initialize", internal.NewProcessor(v.VersionSemantic).Initialize)
	initialize := rotini.MustGet[internal.InitializeFn](rtx, "initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}
}
