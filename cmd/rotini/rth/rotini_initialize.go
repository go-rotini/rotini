package rth

import (
	"context"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*rotiniInitializeHandlers)(nil)

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
	io := rotini.MustGet[*rtk.IO](rtx, "io")

	var inputs rtg.RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		io.Stderr.Println("rotini:", err)
		rtx.Exit(1)
		return
	}
	in := inputs.RotiniInitialize

	if in.Flags.Help {
		io.Stdout.Println(rtg.HelpRotiniInitialize)
		return
	}

	name, format, into := in.Arguments.Name, in.Flags.Format, in.Flags.Into

	// The wizard runs when asked for explicitly (-i / --interactive), or for a bare
	// `rotini init` (no name) at an interactive terminal. With neither a name nor a
	// terminal to prompt at, the name argument is required.
	wizard := in.Flags.Interactive
	if !wizard && name == "" {
		if rotini.MustGet[*rtk.Terminal](rtx, "terminal").StdinIsTerminal() {
			wizard = true
		} else {
			io.Stderr.Println("Error: a name argument is required (or use -i for the interactive wizard)")
			rtx.Exit(1)
			return
		}
	}

	if wizard {
		var err error
		if name, format, into, err = runInitWizard(rotini.MustGet[*rtk.Prompter](rtx, "prompt")); err != nil {
			io.Stderr.Println("Error:", err)
			rtx.Exit(1)
			return
		}
	}

	if err := internal.Initialize(name, format, in.Flags.Force, into); err != nil {
		io.Stderr.Println("Error:", err)
		rtx.Exit(1)
		return
	}
}

// runInitWizard walks the user through the inputs `rotini init` needs — the binary
// name, the spec-file format, and an optional parent CLI to compose into — via the
// bound [rtk.Prompter], returning them for [internal.Initialize]. It is the
// interactive counterpart to the name argument and the --format/--into flags. The
// name is re-prompted until non-empty; any read error (e.g. EOF) is returned.
func runInitWizard(pr *rtk.Prompter) (name, format, into string, err error) {
	for {
		if name, err = pr.Line("Project (binary) name"); err != nil {
			return "", "", "", err
		}
		if name = strings.TrimSpace(name); name != "" {
			break
		}
	}

	formats := []string{"yaml", "json"}
	i, err := pr.Select("Spec file format", formats)
	if err != nil {
		return "", "", "", err
	}
	format = formats[i]

	compose, err := pr.Confirm("Compose into an existing parent CLI?", false)
	if err != nil {
		return "", "", "", err
	}
	if compose {
		if into, err = pr.Line("Parent CLI name"); err != nil {
			return "", "", "", err
		}
		into = strings.TrimSpace(into)
	}
	return name, format, into, nil
}
