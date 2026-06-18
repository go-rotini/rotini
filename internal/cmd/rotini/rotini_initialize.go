package rotini

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/tortellini"
)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

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

	v := rotini.MustGet[*tortellini.Versioner](rtx, tortellini.KeyVersioner)
	rtx.BindIfAbsent("initialize", internal.NewProcessor(v.VersionSemantic).Initialize)
	initialize := rotini.MustGet[internal.InitializeFn](rtx, "initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}
}
