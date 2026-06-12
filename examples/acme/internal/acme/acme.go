package acme

// The root handler: the wired -h/--help and -v/--version experience, plus the
// Suggestor pattern — a ParseError carries the offending token and its
// vocabulary, and the bound Suggestor turns them into "did you mean".

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini"
)

type acmeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*acmeHandlers)(nil)

func (*acmeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs AcmeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
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
		fmt.Fprintln(rtx.Stdout, HelpAcme)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}

	flags := inputs.Acme.Flags
	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, HelpAcme)
		rtx.SignalExit(0)
	case flags.Version:
		v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
		fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
		rtx.SignalExit(0)
	default:
		fmt.Fprintln(rtx.Stdout, HelpAcme)
		rtx.SignalExit(1)
	}
}
