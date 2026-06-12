package acme

// The version handler: the Versioner resolves one version string whether the
// binary was built with -ldflags (main.go binds it) or installed by module
// path (debug.BuildInfo).

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type acmeVersionHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*acmeVersionHandlers)(nil)

func (*acmeVersionHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)

	var inputs AcmeVersionInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n", err)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}
	if inputs.AcmeVersion.Flags.Help {
		fmt.Fprintln(rtx.Stdout, HelpAcmeVersion)
		rtx.SignalExit(0)
		return
	}

	v := rotini.MustGet[*rotini.Versioner](rtx, rotini.KeyVersioner)
	fmt.Fprintf(rtx.Stdout, "v%s\n", v.VersionSemantic)
}
