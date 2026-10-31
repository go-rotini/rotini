package rotini

import (
	"context"
	"fmt"
	"runtime"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*rotiniVersionHandler)(nil)

type rotiniVersionHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniVersionHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniVersionInputs]()
	if err != nil {
		return inputError(err)
	}

	version := "v" + rtx.Version()
	if inputs.RotiniVersion.Flags.Format != "json" {
		if _, err := fmt.Fprintln(rtx.Stdout, version); err != nil {
			return writeError(err)
		}
		rtx.HaltWithCode(0)
		return nil
	}

	out := RotiniVersionOutput{Version: version, GoVersion: runtime.Version()}
	if info, ok := readBuildInfo(); ok {
		out.ModulePath, out.ModuleVersion = info.Main.Path, info.Main.Version
	}
	if err := rtx.WriteOutput(out, "json", nil); err != nil {
		return err
	}

	return nil
}
