package internal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
)

// helpTemplateName is the editable, seed-once help template file living in the
// help feature's dir (the only user-owned file there). Pruning always keeps it.
const helpTemplateName = "help.txt.tmpl"

// helpNode is one command's help wiring: the embed var/resolver identity plus
// everything needed to produce its .txt. One is produced per command (root +
// every own and composed sub-command). The .txt is produced one of two ways,
// selected by whether the command's verbatim `help` string is set: non-empty →
// write it verbatim; empty → render `data` through the template.
type helpNode struct {
	prefix   string   // PascalCase command prefix; the embed var is "Help"+prefix
	file     string   // .txt file name within the help dir
	paths    []string // resolver case values (name/alias permutations); root = [""]
	name     string   // the command's invocation name, e.g. "rotini generate"
	verbatim string   // command.help — the exact page; "" means render from data
	data     helpData // rendering inputs (used when verbatim == "")
}

// helpHeadings holds the resolved section headings (defaults applied).
type helpHeadings struct {
	Usage, Commands, Arguments, Flags, Examples string
}

// helpData is the per-command template context. Fields are exported because
// text/template can only read exported fields.
type helpData struct {
	Header       string
	Invocation   string // full command path, e.g. "rotini generate"
	Summary      string // command.summary (this command's own one-liner)
	Description  string // command.description (long block)
	Usage        string // command.usage override ("" when unset)
	UsageDerived string // always-computed usage line
	Footer       string
	Headings     helpHeadings
	Commands     []helpCmdRow  // visible direct children
	Arguments    []helpArgRow  // visible own arguments
	Flags        []helpFlagRow // visible own flags
	Examples     []string
}

type helpCmdRow struct {
	Name       string
	Summary    string // ← the child command's help.summary
	Aliases    []string
	Deprecated string
}

type helpArgRow struct {
	Name       string
	Summary    string // ← the argument's summary
	Required   bool
	Variadic   bool
	Default    string
	Enum       []string
	Deprecated string
}

type helpFlagRow struct {
	Identifiers []string
	Summary     string // ← the flag's summary
	Type        string // "" for bool flags
	Required    bool
	Default     string
	Enum        []string
	Deprecated  string
}

// helpVar / helpCase / helpFramework are the data the framework template
// (rotini.go.tmpl) ranges over to emit the embed vars and the Help resolver.
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

// cmdHelp bundles a command's resolved help-presentation fields, which live
// directly on the spec's command (and root). Help, when non-empty, is the exact
// verbatim page; otherwise the page is rendered from the structured fields.
type cmdHelp struct {
	Summary     string
	Description string
	Usage       string
	Header      string
	Footer      string
	Headings    *HelpHeadings
	Examples    []string
	Help        string // verbatim page (command.help / spec.help)
}

// commandHelp gathers the flattened help fields off a sub-command.
func commandHelp(c Command) cmdHelp {
	return cmdHelp{
		Summary: c.Summary, Description: c.Description, Usage: c.Usage,
		Header: c.Header, Footer: c.Footer, Headings: c.Headings,
		Examples: c.Examples, Help: c.Help,
	}
}

// flattenHelp produces a help node per command for the whole resolved tree: the
// root first, then every sub-command in tree order. Each node carries its verbatim
// help (when set) and its built helpData (used when no verbatim help is given).
func flattenHelp(gp *genProgram) []helpNode {
	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     gp.rootName + ".txt",
		paths:    []string{""},
		name:     gp.rootName,
		verbatim: gp.rootHelp.Help,
		data:     buildHelpData(gp.rootName, gp.rootHelp, gp.rootInputs, gp.tree),
	}}

	var walk func(nodes []rnode, identChain [][]string, names []string)
	walk = func(nodes []rnode, identChain [][]string, names []string) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootName + " " + strings.Join(childNames, " ")
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     gp.rootName + "_" + strings.Join(childNames, "_") + ".txt",
				paths:    permute(childChain),
				name:     invocation,
				verbatim: n.help.Help,
				data:     buildHelpData(invocation, n.help, n.inputs, n.children),
			})
			walk(n.children, childChain, childNames)
		}
	}
	walk(gp.tree, nil, nil)
	return out
}

// resolveHeadings applies the section-heading defaults, overriding with any set
// in the spec.
func resolveHeadings(h cmdHelp) helpHeadings {
	hd := helpHeadings{Usage: "Usage", Commands: "Commands", Arguments: "Arguments", Flags: "Flags", Examples: "Examples"}
	if h.Headings == nil {
		return hd
	}
	o := h.Headings
	if o.Usage != "" {
		hd.Usage = o.Usage
	}
	if o.Commands != "" {
		hd.Commands = o.Commands
	}
	if o.Arguments != "" {
		hd.Arguments = o.Arguments
	}
	if o.Flags != "" {
		hd.Flags = o.Flags
	}
	if o.Examples != "" {
		hd.Examples = o.Examples
	}
	return hd
}

// buildHelpData assembles the template context for one command from its help
// fields, inputs, and direct children. Hidden children/inputs are excluded.
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode) helpData {
	d := helpData{
		Invocation:  invocation,
		Headings:    resolveHeadings(h),
		Header:      h.Header,
		Summary:     h.Summary,
		Description: h.Description,
		Usage:       h.Usage,
		Footer:      h.Footer,
		Examples:    h.Examples,
	}
	for _, c := range children {
		if c.hidden {
			continue
		}
		d.Commands = append(d.Commands, helpCmdRow{
			Name:       c.name,
			Summary:    c.help.Summary,
			Aliases:    c.aliases,
			Deprecated: c.deprecated,
		})
	}
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			d.Arguments = append(d.Arguments, helpArgRow{
				Name:       a.Name,
				Summary:    a.Summary,
				Required:   a.Schema != nil && a.Schema.Required,
				Variadic:   isVariadicSchema(a.Schema),
				Default:    schemaDefaultString(a.Schema),
				Enum:       enumOf(a.Schema),
				Deprecated: a.Deprecated,
			})
		}
		for _, f := range inputs.Flags {
			if f.Hidden {
				continue
			}
			d.Flags = append(d.Flags, helpFlagRow{
				Identifiers: flagIdentifiers(f),
				Summary:     f.Summary,
				Type:        flagDisplayType(f.Schema),
				Required:    f.Schema != nil && f.Schema.Required,
				Default:     schemaDefaultString(f.Schema),
				Enum:        enumOf(f.Schema),
				Deprecated:  f.Deprecated,
			})
		}
	}
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children))
	return d
}

// deriveUsage builds the default usage line: invocation, a <command> slot when
// the node has visible children, each visible argument decorated, then [flags]
// when the node has visible flags.
func deriveUsage(invocation string, inputs *Inputs, hasChildren bool) string {
	var b strings.Builder
	b.WriteString(invocation)
	if hasChildren {
		b.WriteString(" <command>")
	}
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			name := a.Name
			if isVariadicSchema(a.Schema) {
				name += "..."
			}
			if a.Schema != nil && a.Schema.Required {
				b.WriteString(" <" + name + ">")
			} else {
				b.WriteString(" [" + name + "]")
			}
		}
	}
	if hasVisibleFlags(inputs) {
		b.WriteString(" [flags]")
	}
	return b.String()
}

func isVariadicSchema(schema *InputSchema) bool {
	return strings.HasPrefix(schemaType(schema), "[]")
}

// flagDisplayType returns the type token shown after a flag's identifiers, or ""
// for bool flags (which don't take a value).
func flagDisplayType(schema *InputSchema) string {
	t := schemaType(schema)
	if t == "bool" {
		return ""
	}
	return t
}

func schemaDefaultString(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	return defaultString(schema.Default)
}

func enumOf(schema *InputSchema) []string {
	if schema == nil {
		return nil
	}
	return schema.Enum
}

// flagIdentifiers returns a flag's CLI identifiers, deriving "--<name>" when none
// are declared (mirrors flagDefsLiteral).
func flagIdentifiers(f FlagInput) []string {
	if len(f.Identifiers) > 0 {
		return f.Identifiers
	}
	return []string{"--" + strings.ReplaceAll(f.Name, "_", "-")}
}

func hasVisibleChildren(children []rnode) bool {
	for _, c := range children {
		if !c.hidden {
			return true
		}
	}
	return false
}

func hasVisibleFlags(inputs *Inputs) bool {
	if inputs == nil {
		return false
	}
	for _, f := range inputs.Flags {
		if !f.Hidden {
			return true
		}
	}
	return false
}

// permute returns every space-joined path through the chain of per-segment
// identifier sets (name + aliases), so the resolver matches an aliased path. An
// empty chain (the root) yields the single empty path.
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

// buildHelpFramework turns the help nodes into the embed vars + resolver cases
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

// writeHelpFiles produces each command's help .txt under the help dir in the
// framework package. When the command's verbatim `help` string is set, that string
// is written byte-exact (only a single trailing newline is normalized); otherwise
// the page is rendered from the structured fields via the template. Either way the
// .txt is rotini-managed — (re)written every pass, skipped when already identical —
// like rotini.go and handlers.go. The help template is loaded (seeding the editable
// default when missing) only when at least one command renders.
func writeHelpFiles(lay layout, dir string, hnodes []helpNode) error {
	if dir == "" {
		return fmt.Errorf("generate.help.dir must not be empty")
	}
	helpDir := filepath.Join(lay.frameworkDir, filepath.FromSlash(dir))
	if err := os.MkdirAll(helpDir, 0o755); err != nil {
		return fmt.Errorf("create help dir %s: %w", helpDir, err)
	}

	renders := false
	for _, hn := range hnodes {
		if hn.verbatim == "" {
			renders = true
			break
		}
	}

	var tmpl *template.Template
	if renders {
		t, err := loadHelpTemplate(helpDir)
		if err != nil {
			return err
		}
		tmpl = t
	}

	for _, hn := range hnodes {
		path := filepath.Join(helpDir, hn.file)
		if hn.verbatim != "" {
			// Verbatim: write exactly what the spec supplied — byte-for-byte, no
			// trailing-newline normalization (the author controls it via YAML).
			if err := writeIfChanged(path, hn.verbatim); err != nil {
				return fmt.Errorf("write help %s: %w", hn.file, err)
			}
			continue
		}
		rendered, err := renderHelpText(tmpl, hn.data)
		if err != nil {
			return fmt.Errorf("render help for %q: %w", hn.name, err)
		}
		if err := writeIfChanged(path, rendered); err != nil {
			return fmt.Errorf("write help %s: %w", hn.file, err)
		}
	}
	return nil
}

// writeIfChanged writes content only when it differs from the file on disk,
// keeping mtimes (and watch loops) stable.
func writeIfChanged(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	return writeFileBytes(path, content)
}

func writeFileBytes(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write help %s: %w", filepath.Base(path), err)
	}
	return nil
}

// loadHelpTemplate reads the framework dir's help.txt.tmpl, seeding it from the
// embedded default when missing, and parses it with the help FuncMap.
func loadHelpTemplate(helpDir string) (*template.Template, error) {
	path := filepath.Join(helpDir, helpTemplateName)
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		def, derr := templateFS.ReadFile("templates/help.txt.tmpl")
		if derr != nil {
			return nil, fmt.Errorf("read embedded default help template: %w", derr)
		}
		if werr := os.WriteFile(path, def, 0o644); werr != nil {
			return nil, fmt.Errorf("seed help template %s: %w", path, werr)
		}
		src = def
	} else if err != nil {
		return nil, fmt.Errorf("read help template %s: %w", path, err)
	}
	tmpl, err := template.New("help.txt.tmpl").Funcs(helpFuncMap()).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse help template %s: %w", path, err)
	}
	return tmpl, nil
}

// renderHelpText renders data through tmpl, aligns tab-separated columns with
// tabwriter, and tidies the result. It is a pure function of (tmpl, data) so
// repeated passes produce byte-identical output.
func renderHelpText(tmpl *template.Template, data helpData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sanitizeHelpData(data)); err != nil {
		return "", fmt.Errorf("execute help template: %w", err)
	}
	aligned, err := tabAlign(buf.String())
	if err != nil {
		return "", err
	}
	return tidy(aligned), nil
}

// sanitizeHelpData replaces tabs/newlines in row text (which would corrupt
// tabwriter columns) with spaces. Block fields (Header/Description/Footer/Usage)
// are left intact. Row slices are copied so the source helpData is not mutated.
func sanitizeHelpData(d helpData) helpData {
	clean := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "\t", " "), "\n", " ")
	}
	d.Commands = append([]helpCmdRow(nil), d.Commands...)
	for i := range d.Commands {
		d.Commands[i].Summary = clean(d.Commands[i].Summary)
		d.Commands[i].Deprecated = clean(d.Commands[i].Deprecated)
	}
	d.Arguments = append([]helpArgRow(nil), d.Arguments...)
	for i := range d.Arguments {
		d.Arguments[i].Summary = clean(d.Arguments[i].Summary)
		d.Arguments[i].Deprecated = clean(d.Arguments[i].Deprecated)
	}
	d.Flags = append([]helpFlagRow(nil), d.Flags...)
	for i := range d.Flags {
		d.Flags[i].Summary = clean(d.Flags[i].Summary)
		d.Flags[i].Deprecated = clean(d.Flags[i].Deprecated)
	}
	return d
}

// tabAlign aligns each contiguous block of tab-separated lines with tabwriter.
func tabAlign(s string) (string, error) {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 4, ' ', 0)
	if _, err := tw.Write([]byte(s)); err != nil {
		return "", fmt.Errorf("tabwriter write: %w", err)
	}
	if err := tw.Flush(); err != nil {
		return "", fmt.Errorf("tabwriter flush: %w", err)
	}
	return buf.String(), nil
}

// tidy trims trailing whitespace per line and collapses runs of blank lines to a
// single blank line, then strips leading and trailing blank lines entirely — the
// rendered page ends exactly at its last line of content, with no trailing newline.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.Trim(out, "\n")
}

// helpFuncMap is the deterministic, dependency-free helper set available to the
// help template (an allowlist — no clock/entropy funcs exist to call).
func helpFuncMap() template.FuncMap {
	return template.FuncMap{
		"join":       strings.Join,
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"title":      titleASCII,
		"trim":       strings.TrimSpace,
		"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
		"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
		"replace":    func(old, repl, s string) string { return strings.ReplaceAll(s, old, repl) },
		"indent":     indentLines,
		"repeat":     func(n int, s string) string { return strings.Repeat(s, n) },
		"default": func(def, s string) string {
			if s == "" {
				return def
			}
			return s
		},
		"contains":  func(substr, s string) bool { return strings.Contains(s, substr) },
		"hasPrefix": func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
		"hasSuffix": func(suffix, s string) bool { return strings.HasSuffix(s, suffix) },
		"first": func(elems []string) string {
			if len(elems) == 0 {
				return ""
			}
			return elems[0]
		},
		"last": func(elems []string) string {
			if len(elems) == 0 {
				return ""
			}
			return elems[len(elems)-1]
		},
	}
}

// titleASCII upper-cases the first letter of each word (ASCII only). A local
// implementation: strings.Title is deprecated and golang.org/x/text would add a
// dependency.
func titleASCII(s string) string {
	var b strings.Builder
	atWordStart := true
	for _, r := range s {
		if atWordStart && r >= 'a' && r <= 'z' {
			b.WriteRune(r - ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
		atWordStart = r == ' ' || r == '\t' || r == '-' || r == '_'
	}
	return b.String()
}

// indentLines prefixes every non-empty line of s with n spaces.
func indentLines(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}
