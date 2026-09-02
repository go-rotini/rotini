package rotini

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handlers = (*rotiniInitializeHandlers)(nil)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rtx.MustGet[*rotini.Parser](rotini.KeyParser)

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

	version := rtx.MustGet[string](KeyRotiniVersion)
	rtx.BindIfAbsent("initialize", codegen.NewProcessor(version).Initialize)
	initialize := rtx.MustGet[codegen.InitializeFn]("initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	// The scaffold imports the rotini runtime but rotini does not touch the user's
	// go.mod — adding a require is a network operation with a side effect on a file
	// rotini does not own, so it is reported, not performed. Without this the next
	// `go build` fails on a missing module with no hint of what to do.
	fmt.Fprintf(rtx.Stdout, "initialized cmd/%s\n\nNext steps:\n", args.Name)
	fmt.Fprintln(rtx.Stdout, "  go get github.com/go-rotini/rotini    # the runtime the generated code imports")
	fmt.Fprintf(rtx.Stdout, "  go build ./cmd/%s\n", args.Name)
}
