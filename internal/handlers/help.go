package handlers

import (
	"fmt"
	"strings"

	rotini "github.com/go-rotini/rotini/internal/rotini"
	"github.com/go-rotini/rotini/rtk"
)

// HelpHandlerImpl handles `rotini help [command...]`. Prints the
// help text for the named command; unknown commands fall back to the
// root help with a non-zero exit code.
type HelpHandlerImpl struct{}

// Run satisfies [rotini.HelpHandler].
func (h *HelpHandlerImpl) Run(ctx rotini.HelpCtx, inputs *rotini.HelpInputs) error {
	io := rtk.Get[*rtk.IO](ctx.Registry, "io")

	if inputs.Flags.Help {
		fprintln(io.Stdout, getHelp(helpKeyHelp))
		return nil
	}

	commandPath := strings.Join(inputs.Arguments.Command, " ")
	key, ok := helpKeyForCommand(commandPath)
	if !ok {
		_, _ = io.Stderr.Println(fmt.Sprintf("Error: unknown command path %q", commandPath))
		fprintln(io.Stdout, getHelp(helpKeyRoot))
		return &rtk.EarlyExit{Code: 1}
	}
	fprintln(io.Stdout, getHelp(key))
	return nil
}

// helpKeyForCommand maps the user-supplied command path (e.g.
// "generate", "gen", "" for root) to the [helpKey] for that command.
// Returns (root, true) for the empty path and false for unknown
// paths so the caller can decide how to render the failure.
func helpKeyForCommand(path string) (helpKey, bool) {
	switch path {
	case "":
		return helpKeyRoot, true
	case "generate", "gen":
		return helpKeyGenerate, true
	case "initialize", "init":
		return helpKeyInitialize, true
	case "validate", "val":
		return helpKeyValidate, true
	case "version":
		return helpKeyVersion, true
	case "completion":
		return helpKeyCompletion, true
	case "help":
		return helpKeyHelp, true
	default:
		return 0, false
	}
}
