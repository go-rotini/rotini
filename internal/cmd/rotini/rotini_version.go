package rotini

import (
	"context"
	"fmt"
	"runtime"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniVersionHandler)(nil)

type rotiniVersionHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniVersionHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniVersionInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	version := "v" + rtx.Version()
	if inputs.RotiniVersion.Flags.Format != "json" {
		if _, err := fmt.Fprintln(rtx.Stdout, version); err != nil {
			haltWithWriteError(rtx, err)
			return
		}
		rtx.HaltWithCode(0)
		return
	}

	out := RotiniVersionOutput{Version: version, GoVersion: runtime.Version()}
	if info, ok := readBuildInfo(); ok {
		out.ModulePath, out.ModuleVersion = info.Main.Path, info.Main.Version
	}
	if err := rtx.WriteOutput(out, "json", nil); err != nil {
		rtx.HaltWith(err)
	}
}
