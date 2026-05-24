package rotini

import (
	"fmt"
	"io"
	"strings"
)

// Usage renders help text for the command rotini resolved for this invocation:
// summary, usage line, description, then sub-commands, arguments, flags, and any
// plugin commands as aligned two-column lists. It reads the resolved chain from
// rtx, so a handler can opt into standard usage on -h/--help or on a parse error
// without re-deriving it from the Definition:
//
//	in, err := rotini.Parse[rtg.Inputs](rtx)
//	if err != nil {
//		fmt.Fprintln(os.Stderr, err)
//		fmt.Fprint(os.Stderr, rotini.Usage(rtx))
//		rotini.Exit(rtx, 2)
//		return
//	}
//
// CLIs that hand-author their help text simply ignore it. Usage returns "" for a
// nil context or before the runtime has resolved a command.
func Usage(rtx Context) string {
	if rtx == nil || len(rtx.chain) == 0 {
		return ""
	}
	var b strings.Builder
	writeUsage(&b, rtx.chain)
	return b.String()
}

// writeUsage renders help for the leaf of chain: an optional summary, the usage
// line, an optional description, then the sub-commands, arguments, and flags
// (each as an aligned two-column list), and a footer pointing at per-command
// help. Sections with nothing to show are omitted.
func writeUsage(w io.Writer, chain []frame) {
	leaf := chain[len(chain)-1]
	path := make([]string, len(chain))
	for i, f := range chain {
		path[i] = f.name
	}
	full := strings.Join(path, " ")

	if leaf.summary != "" {
		fmt.Fprintf(w, "%s\n\n", leaf.summary)
	}

	fmt.Fprintf(w, "Usage:\n  %s", full)
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

	if leaf.description != "" {
		fmt.Fprintf(w, "\n%s\n", leaf.description)
	}

	if len(leaf.commands) > 0 {
		rows := make([][2]string, 0, len(leaf.commands))
		for _, c := range leaf.commands {
			name := c.Name
			if len(c.Aliases) > 0 {
				name += ", " + strings.Join(c.Aliases, ", ")
			}
			rows = append(rows, [2]string{name, c.Summary})
		}
		fmt.Fprint(w, "\nCommands:\n")
		writeColumns(w, rows)
	}

	if len(leaf.arguments) > 0 {
		rows := make([][2]string, 0, len(leaf.arguments))
		for _, a := range leaf.arguments {
			rows = append(rows, [2]string{a.Name, argHelp(a)})
		}
		fmt.Fprint(w, "\nArguments:\n")
		writeColumns(w, rows)
	}

	if len(leaf.flags) > 0 {
		rows := make([][2]string, 0, len(leaf.flags))
		for _, f := range leaf.flags {
			left := strings.Join(f.Identifiers, ", ")
			if f.Type != "" && f.Type != "bool" {
				left += " " + flagTypeHint(f.Type)
			}
			rows = append(rows, [2]string{left, flagHelp(f)})
		}
		fmt.Fprint(w, "\nFlags:\n")
		writeColumns(w, rows)
	}

	if len(leaf.remotes) > 0 {
		rows := make([][2]string, 0, len(leaf.remotes))
		for _, r := range leaf.remotes {
			name := r.Name
			if len(r.Aliases) > 0 {
				name += ", " + strings.Join(r.Aliases, ", ")
			}
			rows = append(rows, [2]string{name, "(plugin: " + r.Binary + ")"})
		}
		fmt.Fprint(w, "\nPlugin commands:\n")
		writeColumns(w, rows)
	}

	if len(leaf.commands) > 0 {
		fmt.Fprintf(w, "\nUse %q for more information about a command.\n", full+" <command> --help")
	}
}

// writeColumns prints aligned "  left   right" rows; rows with an empty right
// column print just the left value.
func writeColumns(w io.Writer, rows [][2]string) {
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	for _, r := range rows {
		if r[1] == "" {
			fmt.Fprintf(w, "  %s\n", r[0])
		} else {
			fmt.Fprintf(w, "  %-*s   %s\n", width, r[0], r[1])
		}
	}
}

func flagHelp(f FlagDef) string {
	help := f.Description
	if f.Default != "" {
		help = strings.TrimSpace(fmt.Sprintf(`%s (default %q)`, help, f.Default))
	}
	return help
}

func argHelp(a ArgDef) string {
	help := a.Description
	if len(a.Enum) > 0 {
		help = strings.TrimSpace(fmt.Sprintf("%s (one of: %s)", help, strings.Join(a.Enum, ", ")))
	}
	return help
}

// flagTypeHint is the type word shown after a non-bool flag's identifiers.
func flagTypeHint(t string) string {
	switch t {
	case "[]string":
		return "strings"
	case "time.Duration":
		return "duration"
	default:
		return t
	}
}
