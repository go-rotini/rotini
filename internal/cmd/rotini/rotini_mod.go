package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
)

var _ rotini.Handlers = (*rotiniModHandlers)(nil)

type rotiniModHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniModHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniModInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	args := inputs.RotiniMod.Arguments
	flags := inputs.RotiniMod.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpRotiniMod)
		rtx.SignalExit(0)
		return
	}

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	rtx.BindIfAbsent("mod", internal.NewProcessor(v.VersionSemantic).Mod)
	mod := rotini.MustGet[internal.ModFn](rtx, "mod")

	if err := mod(args.SpecFilePath); err != nil {
		rtx.RecordError(err)
		rtx.SignalExit(1)
		return
	}

	fmt.Fprintln(rtx.Stdout, "ok: external refs pinned to .rotini.lock")
	rtx.SignalExit(0)
}
