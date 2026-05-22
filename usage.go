package rotini

import (
	"fmt"
	"io"
	"strings"
)

// printUsage renders a minimal usage summary for the leaf of chain: the
// invocation path, sub-commands, and flags. Descriptions are omitted until the
// spec carries them; this keeps a bare invocation and -h/--help from being
// silent.
func printUsage(w io.Writer, chain []frame) {
	leaf := chain[len(chain)-1]

	path := make([]string, len(chain))
	for i, f := range chain {
		path[i] = f.name
	}
	fmt.Fprintf(w, "Usage:\n  %s", strings.Join(path, " "))
	if len(leaf.commands) > 0 {
		fmt.Fprint(w, " <command>")
	}
	if len(leaf.flags) > 0 {
		fmt.Fprint(w, " [flags]")
	}
	for _, a := range leaf.arguments {
		if a.Variadic {
			fmt.Fprintf(w, " [%s...]", a.Name)
		} else {
			fmt.Fprintf(w, " <%s>", a.Name)
		}
	}
	fmt.Fprintln(w)

	if len(leaf.commands) > 0 {
		fmt.Fprint(w, "\nCommands:\n")
		for _, c := range leaf.commands {
			name := c.Name
			if len(c.Aliases) > 0 {
				name += " (" + strings.Join(c.Aliases, ", ") + ")"
			}
			fmt.Fprintf(w, "  %s\n", name)
		}
	}
	if len(leaf.flags) > 0 {
		fmt.Fprint(w, "\nFlags:\n")
		for _, f := range leaf.flags {
			fmt.Fprintf(w, "  %s\n", strings.Join(f.Identifiers, ", "))
		}
	}
}
