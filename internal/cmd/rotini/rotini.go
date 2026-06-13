package rotini

import (
	"context"
	"errors"
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
		// Parse failures are data: the ParseError carries the offending token and
		// its vocabulary, and the bound Suggestor (see main.go) turns them into a
		// suggestion. Remove the bind — or this block — to opt out.
		msg := err.Error()
		var parseErr *rotini.ParseError
		if errors.As(err, &parseErr) && parseErr.Token != "" {
			if suggestor, ok := rotini.Get[*rotini.Suggestor](rtx, rotini.KeySuggestor); ok {
				if hits := suggestor.Suggest(parseErr.Token, parseErr.Candidates); len(hits) > 0 {
					msg += fmt.Sprintf("\n\nDid you mean %q?", hits[0])
				}
			}
		}
		// The env channel may not have bound on a usage error, so only the
		// --no-styles flag can speak for no-styles here.
		fmt.Fprintf(rtx.Stderr, "Error: %s\n\n", msg)
		fmt.Fprintln(rtx.Stdout, rotini.StripStyles(HelpRotini, func() bool {
			return inputs.Rotini.Flags.NoStyles
		}))
		rtx.SignalExit(rotini.ExitUsage) // bad input → the conventional usage exit code
		return
	}

	flags := inputs.Rotini.Flags
	env := inputs.Rotini.Env

	// Strip the help page's spec-authored styling when any no-styles signal is
	// set: the --no-styles flag, $ROTINI_NO_STYLES, or a CI environment ($CI).
	noStyles := func() bool { return flags.NoStyles || env.NoStyles || env.Ci }

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
