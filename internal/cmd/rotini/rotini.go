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
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs RotiniInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
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
		fmt.Fprintf(rtx.Stderr, "Error: %s\n\n", msg)
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.SignalExit(rotini.ExitUsage) // bad input → the conventional usage exit code
		return
	}

	flags := inputs.Rotini.Flags
	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.SignalExit(0)
		return
	case flags.Version:
		v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
		fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
		rtx.SignalExit(0)
		return
	default:
		fmt.Fprintln(rtx.Stdout, HelpRotini)
		rtx.SignalExit(1)
		return
	}
}
