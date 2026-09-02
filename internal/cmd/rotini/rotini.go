package rotini

import (
	"context"
	"fmt"
	"regexp"
	"runtime/debug"

	"github.com/go-rotini/rotini"
)

const (
	KeyRotiniVersion = "versioner"
)

var (
	readBuildInfo     = debug.ReadBuildInfo
	semanticVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)
)

var _ rotini.Handlers = (*rotiniHandlers)(nil)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func ResolveVersion(ldflagVersion string) string {
	version := ldflagVersion
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}

	if match := semanticVersionRe.FindStringSubmatch(version); match != nil {
		version = match[1]
	}

	return version
}

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[RotiniInputs](rtx)
	flags := inputs.Rotini.Flags
	env := inputs.Rotini.Env

	help := HelpRotini
	if flags.Nostyles || env.Nostyles || env.Ci {
		help = rotini.Strip(HelpRotini)
	}

	if err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %s\n\n", err.Error())
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(1)
		return
	}

	switch {
	case flags.Help:
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(0)
		return
	case flags.Version:
		version := rtx.MustGet[string](KeyRotiniVersion)
		fmt.Fprintf(rtx.Stdout, "v%s\n", version)
		rtx.SignalExit(0)
		return
	default:
		fmt.Fprintln(rtx.Stdout, help)
		rtx.SignalExit(1)
		return
	}
}
