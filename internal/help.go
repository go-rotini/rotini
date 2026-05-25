package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// helpNode is one command's fully-resolved help model: enough to render its
// help text and to wire up its generated embed var + resolver case. One is
// produced per command (root + every own and composed sub-command).
type helpNode struct {
	prefix    string      // PascalCase command prefix; the var is "Help"+prefix
	file      string      // .txt file name within the help dir
	paths     []string    // resolver case values (name/alias permutations); root = [""]
	pathNames []string    // canonical command names, root→leaf (for the auto usage line)
	short     string      // normalized short_description (falls back to summary)
	long      string      // normalized long_description (falls back to description)
	usage     string      // usage override; empty means auto-derive
	examples  []string    // example invocation lines
	homepage  string      // root only; the "Find more information at: …" line
	inputs    *Inputs     // for the Arguments/Flags sections + auto usage
	children  []helpChild // for the Commands section
}

// helpChild is a sub-command as shown in a parent's Commands section.
type helpChild struct {
	idents []string // name followed by aliases
	short  string   // the child's short description
}

// helpVar / helpCase / helpFramework are the data the framework template
// (rotini.go.tmpl) ranges over to emit the embed vars and the Help resolver.
// Fields are exported because text/template can only read exported fields.
type helpVar struct {
	Name  string // Go var name, e.g. "HelpRotiniGenerate"
	Embed string // //go:embed path, e.g. "help/rotini_generate.txt"
}

type helpCase struct {
	PathsLiteral string // case values, e.g. `"generate", "gen"` (root: `""`)
	Var          string // the var returned for these paths
}

type helpFramework struct {
	Vars  []helpVar
	Cases []helpCase
}

// flattenHelp produces a help model per command for the whole resolved tree:
// the root first, then every sub-command in tree order.
func flattenHelp(gp *genProgram) []helpNode {
	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     gp.rootName + ".txt",
		paths:    []string{""},
		short:    gp.rootSummary,
		long:     gp.rootDescription,
		usage:    gp.rootUsage,
		examples: gp.rootExamples,
		homepage: gp.rootHomepage,
		inputs:   gp.rootInputs,
		children: childrenOf(gp.tree),
	}}

	var walk func(nodes []rnode, identChain [][]string, names []string)
	walk = func(nodes []rnode, identChain [][]string, names []string) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			out = append(out, helpNode{
				prefix:    n.prefix,
				file:      gp.rootName + "_" + strings.Join(childNames, "_") + ".txt",
				paths:     permute(childChain),
				pathNames: childNames,
				short:     n.summary,
				long:      n.description,
				usage:     n.usage,
				examples:  n.examples,
				inputs:    n.inputs,
				children:  childrenOf(n.children),
			})
			walk(n.children, childChain, childNames)
		}
	}
	walk(gp.tree, nil, nil)
	return out
}

// childrenOf projects a node's children into the Commands-section rows.
func childrenOf(nodes []rnode) []helpChild {
	out := make([]helpChild, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, helpChild{idents: append([]string{n.name}, n.aliases...), short: n.summary})
	}
	return out
}

// permute returns every space-joined path through the chain of per-segment
// identifier sets (name + aliases), so the resolver matches an aliased path.
// An empty chain (the root) yields the single empty path.
func permute(chain [][]string) []string {
	out := []string{""}
	for _, seg := range chain {
		var next []string
		for _, prefix := range out {
			for _, id := range seg {
				if prefix == "" {
					next = append(next, id)
				} else {
					next = append(next, prefix+" "+id)
				}
			}
		}
		out = next
	}
	return out
}

// buildHelpFramework turns the help model into the embed vars + resolver cases
// the framework template emits.
func buildHelpFramework(hnodes []helpNode, dir string) *helpFramework {
	h := &helpFramework{}
	for _, hn := range hnodes {
		name := "Help" + hn.prefix
		h.Vars = append(h.Vars, helpVar{Name: name, Embed: dir + "/" + hn.file})
		quoted := make([]string, len(hn.paths))
		for i, p := range hn.paths {
			quoted[i] = strconv.Quote(p)
		}
		h.Cases = append(h.Cases, helpCase{PathsLiteral: strings.Join(quoted, ", "), Var: name})
	}
	return h
}

// writeHelpFiles renders each command's help text and writes it under the help
// dir in the framework package. The whole dir is framework-owned: it is removed
// and rewritten each pass, so help for a removed command does not linger.
func writeHelpFiles(lay layout, dir string, hnodes []helpNode, rootName string) error {
	if dir == "" {
		return fmt.Errorf("generate.help.dir must not be empty")
	}
	helpDir := filepath.Join(lay.frameworkDir, filepath.FromSlash(dir))
	if err := os.RemoveAll(helpDir); err != nil {
		return fmt.Errorf("clear help dir %s: %w", helpDir, err)
	}
	if err := os.MkdirAll(helpDir, 0o755); err != nil {
		return fmt.Errorf("create help dir %s: %w", helpDir, err)
	}
	for _, hn := range hnodes {
		path := filepath.Join(helpDir, hn.file)
		if err := os.WriteFile(path, []byte(renderHelpText(hn, rootName)), 0o644); err != nil {
			return fmt.Errorf("write help %s: %w", hn.file, err)
		}
	}
	return nil
}

// renderHelpText assembles one command's help text from its model. Structural
// sections (usage, arguments, flags, commands, footer) are derived from spec
// data; the author supplies only the description, examples, and an optional
// usage override. The result has no trailing newline (handlers print it with a
// newline-adding Println, matching the previous hand-written help consts).
func renderHelpText(hn helpNode, rootName string) string {
	var b strings.Builder

	if header := firstNonEmpty(hn.long, hn.short); header != "" {
		b.WriteString(header)
		if hn.homepage != "" {
			b.WriteString("\n\nFind more information at: " + hn.homepage)
		}
		b.WriteString("\n\n")
	} else if hn.homepage != "" {
		b.WriteString("Find more information at: " + hn.homepage + "\n\n")
	}

	usage := usageLine(rootName, hn)
	b.WriteString("Usage:\n  " + strings.ReplaceAll(usage, "\n", "\n  ") + "\n")

	if hn.inputs != nil && len(hn.inputs.Arguments) > 0 {
		b.WriteString("\n" + section("Arguments", argRows(hn.inputs.Arguments)))
	}
	if hn.inputs != nil && len(hn.inputs.Flags) > 0 {
		b.WriteString("\n" + section("Flags", flagRows(hn.inputs.Flags)))
	}
	if len(hn.children) > 0 {
		b.WriteString("\n" + section("Commands", childRows(hn.children)))
	}
	if len(hn.examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range hn.examples {
			b.WriteString("  " + e + "\n")
		}
	}
	if len(hn.children) > 0 {
		b.WriteString("\nUse \"" + rootName + " help <command>\" for more information about a command.\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// usageLine returns the command's usage override, or an auto-derived line:
// "<root> <path…> [<command>] [<args>…] [flags]".
func usageLine(rootName string, hn helpNode) string {
	if hn.usage != "" {
		return hn.usage
	}
	parts := append([]string{rootName}, hn.pathNames...)
	if len(hn.children) > 0 {
		parts = append(parts, "<command>")
	}
	if hn.inputs != nil {
		for _, a := range hn.inputs.Arguments {
			parts = append(parts, argToken(a))
		}
		if len(hn.inputs.Flags) > 0 {
			parts = append(parts, "[flags]")
		}
	}
	return strings.Join(parts, " ")
}

// argToken renders a positional argument in a usage line: <name> when required,
// [<name>…] when variadic, otherwise [<name>].
func argToken(a ArgumentInput) string {
	switch {
	case strings.HasPrefix(schemaType(a.Schema), "[]"):
		return "[<" + a.Name + ">…]"
	case a.Schema != nil && a.Schema.Required:
		return "<" + a.Name + ">"
	default:
		return "[<" + a.Name + ">]"
	}
}

// argRows builds the Arguments-section rows: the name and its description, with
// a declared default appended.
func argRows(args []ArgumentInput) [][2]string {
	rows := make([][2]string, 0, len(args))
	for _, a := range args {
		desc := ""
		var def any
		if a.Schema != nil {
			desc = a.Schema.Description
			def = a.Schema.Default
		}
		if d := defaultString(def); d != "" {
			desc = appendDefault(desc, d)
		}
		rows = append(rows, [2]string{a.Name, desc})
	}
	return rows
}

// flagRows builds the Flags-section rows: the comma-joined identifiers (or the
// auto-derived "--<name>") and the description, with a declared default appended.
func flagRows(flags []FlagInput) [][2]string {
	rows := make([][2]string, 0, len(flags))
	for _, f := range flags {
		ids := f.Identifiers
		if len(ids) == 0 {
			ids = []string{"--" + strings.ReplaceAll(f.Name, "_", "-")}
		}
		desc := ""
		var def any
		if f.Schema != nil {
			desc = f.Schema.Description
			def = f.Schema.Default
		}
		if d := defaultString(def); d != "" {
			desc = appendDefault(desc, d)
		}
		rows = append(rows, [2]string{strings.Join(ids, ","), desc})
	}
	return rows
}

// childRows builds the Commands-section rows: "name;alias…" and the child's
// short description.
func childRows(children []helpChild) [][2]string {
	rows := make([][2]string, 0, len(children))
	for _, c := range children {
		rows = append(rows, [2]string{strings.Join(c.idents, ";"), c.short})
	}
	return rows
}

// section renders a titled, column-aligned block. Each row's left column is
// padded to the section's widest left value plus a four-space gutter; a row
// with no description prints just its left column.
func section(title string, rows [][2]string) string {
	maxL := 0
	for _, r := range rows {
		if len(r[0]) > maxL {
			maxL = len(r[0])
		}
	}
	var b strings.Builder
	b.WriteString(title + ":\n")
	for _, r := range rows {
		b.WriteString("  " + r[0])
		if r[1] != "" {
			b.WriteString(strings.Repeat(" ", maxL-len(r[0])+4) + r[1])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// appendDefault appends a "(default: …)" note to a description.
func appendDefault(desc, def string) string {
	note := "(default: " + def + ")"
	if desc == "" {
		return note
	}
	return desc + " " + note
}

// firstNonEmpty returns the first non-empty string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
