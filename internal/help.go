package internal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
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
func writeHelpFiles(lay layout, dir string, hnodes []helpNode, rootName string, tmpl *template.Template) error {
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
		text, err := renderHelpText(tmpl, hn, rootName)
		if err != nil {
			return err
		}
		path := filepath.Join(helpDir, hn.file)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return fmt.Errorf("write help %s: %w", hn.file, err)
		}
	}
	return nil
}

// helpRow is one aligned row in a help section (a flag/argument/command and its
// description). Fields are exported so the help template can read them.
type helpRow struct {
	Left  string
	Right string
}

// helpData is the per-command view passed to the help template. Fields are
// exported for template access; the contract is documented at the top of
// internal/templates/help.text.tmpl.
type helpData struct {
	Root      string
	Name      string
	Path      string
	Short     string
	Long      string
	Intro     string
	Usage     string
	Homepage  string
	Examples  []string
	Arguments []helpRow
	Flags     []helpRow
	Commands  []helpRow
	Footer    string
	ArgWidth  int
	FlagWidth int
	CmdWidth  int
}

// helpFuncs are the template helpers available to both the built-in and any
// user-supplied help template.
var helpFuncs = template.FuncMap{
	// indent prefixes every line of s with n spaces.
	"indent": func(n int, s string) string {
		pad := strings.Repeat(" ", n)
		return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
	},
	// row renders an aligned "  left<gutter>right" line. With no right value it
	// prints just the un-padded left column, so there is no trailing whitespace.
	"row": func(left, right string, width int) string {
		if right == "" {
			return "  " + left
		}
		return fmt.Sprintf("  %-*s    %s", width, left, right)
	},
}

// loadHelpTemplate parses the help template: the user-supplied one at custom
// (resolved against moduleRoot when relative), or rotini's built-in template.
func loadHelpTemplate(custom, moduleRoot string) (*template.Template, error) {
	if custom != "" {
		path := custom
		if !filepath.IsAbs(path) {
			path = filepath.Join(moduleRoot, filepath.FromSlash(custom))
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read help template %s: %w", path, err)
		}
		t, err := template.New(filepath.Base(path)).Funcs(helpFuncs).Parse(string(src))
		if err != nil {
			return nil, fmt.Errorf("parse help template %s: %w", path, err)
		}
		return t, nil
	}
	src, err := templateFS.ReadFile("templates/help.text.tmpl")
	if err != nil {
		return nil, fmt.Errorf("read built-in help template: %w", err)
	}
	t, err := template.New("help.text.tmpl").Funcs(helpFuncs).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse built-in help template: %w", err)
	}
	return t, nil
}

// renderHelpText renders one command's help text through tmpl. Trailing newlines
// are trimmed (handlers print it with a newline-adding Println, matching the
// previous hand-written help consts).
func renderHelpText(tmpl *template.Template, hn helpNode, rootName string) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, buildHelpData(hn, rootName)); err != nil {
		return "", fmt.Errorf("render help %s: %w", hn.file, err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// buildHelpData projects a help model node into the template's view, deriving
// the intro block, usage line, aligned rows, and footer.
func buildHelpData(hn helpNode, rootName string) helpData {
	intro := firstNonEmpty(hn.long, hn.short)
	if hn.homepage != "" {
		if intro != "" {
			intro += "\n\nFind more information at: " + hn.homepage
		} else {
			intro = "Find more information at: " + hn.homepage
		}
	}
	footer := ""
	if len(hn.children) > 0 {
		footer = "Use \"" + rootName + " help <command>\" for more information about a command."
	}
	name := ""
	if n := len(hn.pathNames); n > 0 {
		name = hn.pathNames[n-1]
	}
	var args, flags []helpRow
	if hn.inputs != nil {
		args = argRows(hn.inputs.Arguments)
		flags = flagRows(hn.inputs.Flags)
	}
	cmds := childRows(hn.children)
	return helpData{
		Root:      rootName,
		Name:      name,
		Path:      strings.Join(hn.pathNames, " "),
		Short:     hn.short,
		Long:      hn.long,
		Intro:     intro,
		Usage:     usageLine(rootName, hn),
		Homepage:  hn.homepage,
		Examples:  hn.examples,
		Arguments: args,
		Flags:     flags,
		Commands:  cmds,
		Footer:    footer,
		ArgWidth:  colWidth(args),
		FlagWidth: colWidth(flags),
		CmdWidth:  colWidth(cmds),
	}
}

// colWidth returns the width of the widest Left column across rows.
func colWidth(rows []helpRow) int {
	w := 0
	for _, r := range rows {
		if len(r.Left) > w {
			w = len(r.Left)
		}
	}
	return w
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
func argRows(args []ArgumentInput) []helpRow {
	rows := make([]helpRow, 0, len(args))
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
		rows = append(rows, helpRow{Left: a.Name, Right: desc})
	}
	return rows
}

// flagRows builds the Flags-section rows: the comma-joined identifiers (or the
// auto-derived "--<name>") and the description, with a declared default appended.
func flagRows(flags []FlagInput) []helpRow {
	rows := make([]helpRow, 0, len(flags))
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
		rows = append(rows, helpRow{Left: strings.Join(ids, ","), Right: desc})
	}
	return rows
}

// childRows builds the Commands-section rows: "name;alias…" and the child's
// short description.
func childRows(children []helpChild) []helpRow {
	rows := make([]helpRow, 0, len(children))
	for _, c := range children {
		rows = append(rows, helpRow{Left: strings.Join(c.idents, ";"), Right: c.short})
	}
	return rows
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
