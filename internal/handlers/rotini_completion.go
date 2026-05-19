package handlers

import (
	"fmt"

	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// CompletionHandlerImpl handles `rotini completion <shell>`. Prints
// the shell-completion script generated for rotini's own spec.
type CompletionHandlerImpl struct{}

// Run satisfies [rotini.CompletionHandler].
func (h *CompletionHandlerImpl) Run(ctx rotini.CompletionCtx, inputs *rotini.CompletionInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyCompletion))
		return nil
	}

	shell := inputs.Arguments.Shell
	if shell == "" {
		_, _ = io.Stderr.Println("Error: shell argument is required (zsh, bash, fish, powershell, nushell, elvish)")
		return &rtk.EarlyExit{Code: 1}
	}

	script, err := rotini.Completion(rtk.Shell(shell))
	if err != nil {
		_, _ = io.Stderr.Println(err)
		return &rtk.EarlyExit{Code: 1}
	}
	// Use Print (not Println) — completion scripts already carry
	// their own trailing newline conventions per shell.
	if _, err := io.Stdout.Print(script); err != nil {
		return fmt.Errorf("rotini: write completion script: %w", err)
	}
	return nil
}
