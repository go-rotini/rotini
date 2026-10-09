package demo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoHandler)(nil)

type demoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*demoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[DemoInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if inputs.Demo.Flags.Help {
		var path []string
		for _, c := range rtx.CommandChain()[1:] {
			path = append(path, c.Name)
		}

		page, err := Help(path...)
		if err != nil {
			rtx.HaltWith(err)
			return
		}

		if _, err := fmt.Fprintln(rtx.Stdout, page); err != nil {
			rtx.HaltWith(err)
			return
		}

		rtx.HaltWithCode(0)
		return
	}

	if inputs.Demo.Flags.Version {
		version := rtx.Version()
		if version == "" {
			version = "unknown"
		}

		if _, err := fmt.Fprintln(rtx.Stdout, rtx.CommandChain()[0].Name, version); err != nil {
			rtx.HaltWith(err)
			return
		}

		rtx.HaltWithCode(0)
		return
	}
}

func (*demoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
