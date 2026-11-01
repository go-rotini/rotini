package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*demoHandler)(nil)

type demoHandler struct {
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*demoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) error {
	argv, err := rtx.ArgvInputs[DemoInputs]()
	if err != nil {
		return err
	}
	inputs := argv.Values

	if err := rtx.UnknownCommand(); err != nil {
		return err
	}

	if inputs.Demo.Flags.Help {
		page, err := rtx.InvokedHelp()
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(rtx.Stdout, page); err != nil {
			return err
		}

		rtx.HaltWithCode(0)
		return nil
	}

	if inputs.Demo.Flags.Version {
		version := rtx.Version()
		if version == "" {
			version = "unknown"
		}

		if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
			return err
		}

		rtx.HaltWithCode(0)
		return nil
	}

	return nil
}

func (*demoHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	if _, err := rtx.Inputs[DemoInputs](); err != nil {
		return err
	}

	fmt.Fprintln(rtx.Stderr, rtx.Help())
	return rotini.MissingCommandError(rtx)
}
