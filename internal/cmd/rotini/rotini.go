package rotini

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	// Collect, not Parse: one call reconciles every declared channel — the
	// argv flags AND the env channel ($ROTINI_NO_STYLES, $CI) — against the
	// generated BindMeta the program bound at NewProgram time.
	inputs, err := rotini.Collect[RotiniInputs](rtx)
	if err != nil {
		// Record and stop: the program's OnError funnel reports it. With no
		// custom WithOnErrorFn wired (see main.go), rotini's default prints the
		// error to stderr and exits by category (a parse error → ExitUsage).
		// Suggestions ("did you mean") are deliberately NOT here — that is the
		// end-user's own OnError to add, against the bound Suggestor.
		rtx.RecordError(err)
		rtx.SignalExit(2)
		return
	}

	flags := inputs.Rotini.Flags
	env := inputs.Rotini.Env

	// Strip the help page's spec-authored styling when any no-styles signal is
	// set: the --no-styles flag, $ROTINI_NO_STYLES, or a CI environment ($CI).
	noStyles := func() bool { return flags.Nostyles || env.Nostyles || env.Ci }

	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, rotini.StripStyles(HelpRotini, noStyles))
		rtx.SignalExit(0)
		return
	case flags.Version:
		v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
		fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
		rtx.SignalExit(0)
		return
	default:
		fmt.Fprintln(rtx.Stdout, rotini.StripStyles(HelpRotini, noStyles))
		rtx.SignalExit(1)
		return
	}
}
