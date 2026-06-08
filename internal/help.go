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
	"unicode"

	"github.com/go-rotini/fs"
)

// helpTemplateName is the editable, seed-once help template file living in the
// help feature's dir (the only user-owned file there). Pruning always keeps it.
const helpTemplateName = "help.txt.tmpl"

// docFeature describes one doc-rendered codegen feature (help, man, markdown).
// All three share the doc-data pipeline (buildHelpData → renderHelpText) and
// differ only in their file extension, embed-var/resolver names, the editable
// template, and which per-command verbatim spec string escapes the render.
type docFeature struct {
	name      string               // feature key + default dir, e.g. "help"
	noun      string               // word used in the resolver doc comment / error, e.g. "help"
	varPrefix string               // embed-var prefix, e.g. "Help" → HelpRotiniGenerate
	resolver  string               // resolver func name, e.g. "Help"
	ext       string               // output file extension, e.g. ".txt" / ".md"
	tmplFile  string               // editable template file name in the feature dir ("" = none)
	embedTmpl string               // embedded default template path under templates/ ("" = none)
	verbatim  func(cmdHelp) string // the per-command verbatim escape for this feature (nil = none)
	perShell  bool                 // completion: keyed by shell name, not command path
}

var (
	helpFeatureDesc = docFeature{
		name: "help", noun: "help", varPrefix: "Help", resolver: "Help",
		ext: ".txt", tmplFile: helpTemplateName, embedTmpl: "templates/help.txt.tmpl",
		verbatim: func(h cmdHelp) string { return h.Help },
	}
	manFeatureDesc = docFeature{
		name: "man", noun: "man", varPrefix: "Man", resolver: "Man",
		ext: ".txt", tmplFile: "man.txt.tmpl", embedTmpl: "templates/man.txt.tmpl",
		verbatim: func(h cmdHelp) string { return h.Man },
	}
	markdownFeatureDesc = docFeature{
		name: "markdown", noun: "markdown", varPrefix: "Markdown", resolver: "Markdown",
		ext: ".md", tmplFile: "markdown.md.tmpl", embedTmpl: "templates/markdown.md.tmpl",
		verbatim: func(h cmdHelp) string { return h.Markdown },
	}
	// completionFeatureDesc is the group's exception: keyed by shell, no doc-data,
	// no template, no verbatim. Scripts come from completionScript at codegen.
	completionFeatureDesc = docFeature{
		name: "completion", noun: "completion", varPrefix: "Completion", resolver: "Completion",
		ext: ".txt", perShell: true,
	}
)

// completionShells are the shells rotini generates completion scripts for, in a
// deterministic order (matches completionScript's supported set).
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

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

// helpHeadings holds the resolved section headings (defaults applied). Each value
// is rendered verbatim by the template — the trailing ":" lives in the value (the
// defaults carry it), so an override can drop or restyle it.
type helpHeadings struct {
	Usage, Commands, Arguments, Flags, Environment, Configuration, Cascading, Examples string
}

// helpData is the per-command template context. Fields are exported because
// text/template can only read exported fields.
type helpData struct {
	Header        string
	Invocation    string // full command path, e.g. "rotini generate"
	Summary       string // command.summary (this command's own one-liner)
	Description   string // command.description (long block)
	Usage         string // command.usage override ("" when unset)
	UsageDerived  string // always-computed usage line
	Footer        string
	Headings      helpHeadings
	CommandGroups []helpCmdGroup  // visible direct children, bucketed by group (one default-titled bucket when ungrouped)
	Arguments     []helpArgRow    // visible own arguments
	Flags         []helpFlagRow   // visible own flags
	Environment   []helpEnvRow    // visible env-var inputs
	Configuration []helpConfigRow // visible config-value inputs
	Cascading     []helpFlagRow   // visible cascading flags inherited from ancestor commands
	Examples      []string
	ExitStatus    []helpExitRow // documented exit codes (man EXIT STATUS section)
	SeeAlso       []string      // cross-references (man SEE ALSO section)
}

// helpExitRow is one documented exit code in the man EXIT STATUS section. rotini
// renders this data verbatim — it sets no exit code itself (handlers own exits).
type helpExitRow struct {
	Code    int
	Summary string
}

// helpCmdGroup is one bucket of sub-commands in the Commands section. Title is the
// command's `group` value; "" is the ungrouped bucket, which each template heads with
// its own default ("Commands:" / "COMMANDS" / "## Commands"). Titled buckets the
// template formats from Title (its format's convention).
type helpCmdGroup struct {
	Title    string // group label, verbatim; "" = ungrouped (default heading)
	Commands []helpCmdRow
}

type helpCmdRow struct {
	Name       string
	Summary    string // ← the child command's help.summary
	Aliases    []string
	Group      string // ← the child command's `group` (buckets it in the Commands section)
	Deprecated string
}

type helpEnvRow struct {
	Var        string // the environment variable (schema.variable, else snake-upper of the name)
	Summary    string
	Type       string
	Required   bool
	Default    string
	Enum       []string
	Deprecated string
}

type helpConfigRow struct {
	Name       string
	Location   string // "<file>.<key>" / "<key>" — where the value is read from
	Summary    string
	Type       string
	Required   bool
	Default    string
	Enum       []string
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
	Resolver string // resolver func name, e.g. "Help"/"Man"/"Markdown"/"Completion"
	Noun     string // word used in the doc comment + error, e.g. "help"
	PerShell bool   // completion: resolver takes a shell string, not a command path
	Vars     []helpVar
	Cases    []helpCase
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
	ExitStatus  []ExitStatusEntry // command.exit_status (man EXIT STATUS section)
	SeeAlso     []string          // command.see_also (man SEE ALSO section)
	Help        string            // verbatim help page (command.help)
	Man         string            // verbatim man page (command.man)
	Markdown    string            // verbatim markdown page (command.markdown)
}

// commandHelp gathers the flattened doc-fields off a command (root or sub).
func commandHelp(c Command) cmdHelp {
	return cmdHelp{
		Summary: c.Summary, Description: c.Description, Usage: c.Usage,
		Header: c.Header, Footer: c.Footer, Headings: c.Headings,
		Examples: c.Examples, ExitStatus: c.ExitStatus, SeeAlso: c.SeeAlso,
		Help: c.Help, Man: c.Man, Markdown: c.Markdown,
	}
}

// flattenFeature produces a node per command for one doc feature across the whole
// resolved tree: the root first, then every sub-command in tree order. Each node
// carries its verbatim page (the feature's spec escape, when set) and its built
// helpData (the shared doc-data, used when no verbatim page is given). The file
// extension is the feature's; the doc-data is identical across features.
func flattenFeature(gp *genProgram, feat docFeature) []helpNode {
	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     gp.rootName + feat.ext,
		paths:    []string{""},
		name:     gp.rootName,
		verbatim: feat.verbatim(gp.rootHelp),
		data:     buildHelpData(gp.rootName, gp.rootHelp, gp.rootInputs, gp.tree, nil),
	}}

	// cascading carries the cascading flags accumulated from a node's ancestors
	// (the root's own cascading flags seed the root's children, and so on down).
	var walk func(nodes []rnode, identChain [][]string, names []string, cascading []helpFlagRow)
	walk = func(nodes []rnode, identChain [][]string, names []string, cascading []helpFlagRow) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootName + " " + strings.Join(childNames, " ")
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     gp.rootName + "_" + strings.Join(childNames, "_") + feat.ext,
				paths:    permute(childChain),
				name:     invocation,
				verbatim: feat.verbatim(n.help),
				data:     buildHelpData(invocation, n.help, n.inputs, n.children, cascading),
			})
			childCascading := append(append([]helpFlagRow{}, cascading...), cascadingFlagsOf(n.inputs)...)
			walk(n.children, childChain, childNames, childCascading)
		}
	}
	walk(gp.tree, nil, nil, cascadingFlagsOf(gp.rootInputs))
	return out
}

// completionNodes produces one node per supported shell for the completion
// feature: keyed by shell name (the resolver case), file <shell>.txt, embed var
// Completion<Shell>. No doc-data or verbatim — the script comes from
// writeCompletionFiles.
func completionNodes() []helpNode {
	out := make([]helpNode, 0, len(completionShells))
	for _, sh := range completionShells {
		out = append(out, helpNode{
			prefix: toPascalCase(sh),
			file:   sh + completionFeatureDesc.ext,
			paths:  []string{sh},
			name:   sh,
		})
	}
	return out
}

// writeCompletionFiles writes one rotini-managed completion script per supported
// shell under the completion dir, generated from the program name via
// completionScript (the shared source of the bash/zsh/fish templates). Each
// file is (re)written every pass, skipped when already identical.
func writeCompletionFiles(cdir, prog string, nodes []helpNode) error {
	if cdir == "" {
		return fmt.Errorf("generate.features.completion.dir must not be empty")
	}
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		return fmt.Errorf("create completion dir %s: %w", cdir, err)
	}
	for _, n := range nodes {
		script, err := completionScript(prog, n.name)
		if err != nil {
			return fmt.Errorf("generate %s completion: %w", n.name, err)
		}
		if err := writeIfChanged(filepath.Join(cdir, n.file), script); err != nil {
			return fmt.Errorf("write completion %s: %w", n.file, err)
		}
	}
	return nil
}

// resolveHeadings applies the section-heading defaults, overriding with any set
// in the spec.
func resolveHeadings(h cmdHelp) helpHeadings {
	// Defaults carry the trailing ":" so an override is rendered verbatim — a spec
	// author can drop or restyle the colon (the template adds nothing).
	hd := helpHeadings{Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:", Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:", Cascading: "Global Flags:", Examples: "Examples:"}
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
	if o.Environment != "" {
		hd.Environment = o.Environment
	}
	if o.Configuration != "" {
		hd.Configuration = o.Configuration
	}
	if o.Cascading != "" {
		hd.Cascading = o.Cascading
	}
	if o.Examples != "" {
		hd.Examples = o.Examples
	}
	return hd
}

// buildHelpData assembles the template context for one command from its help
// fields, inputs, and direct children. Hidden children/inputs are excluded.
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode, ancestorCascading []helpFlagRow) helpData {
	d := helpData{
		Invocation:  invocation,
		Headings:    resolveHeadings(h),
		Header:      h.Header,
		Summary:     h.Summary,
		Description: h.Description,
		Usage:       h.Usage,
		Footer:      h.Footer,
		Cascading:   ancestorCascading,
		Examples:    h.Examples,
		SeeAlso:     h.SeeAlso,
	}
	for _, e := range h.ExitStatus {
		d.ExitStatus = append(d.ExitStatus, helpExitRow{Code: e.Code, Summary: e.Summary})
	}
	var cmds []helpCmdRow
	for _, c := range children {
		if c.hidden {
			continue
		}
		cmds = append(cmds, helpCmdRow{
			Name:       c.name,
			Summary:    c.help.Summary,
			Aliases:    c.aliases,
			Group:      c.group,
			Deprecated: c.deprecated,
		})
	}
	d.CommandGroups = groupCommands(cmds)
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
			d.Flags = append(d.Flags, flagRow(f))
		}
		for _, e := range inputs.Env {
			if e.Hidden {
				continue
			}
			d.Environment = append(d.Environment, helpEnvRow{
				Var:        envVarLabel(e),
				Summary:    e.Summary,
				Type:       flagDisplayType(e.Schema),
				Required:   e.Schema != nil && e.Schema.Required,
				Default:    schemaDefaultString(e.Schema),
				Enum:       enumOf(e.Schema),
				Deprecated: e.Deprecated,
			})
		}
		for _, c := range inputs.Config {
			if c.Hidden {
				continue
			}
			d.Configuration = append(d.Configuration, helpConfigRow{
				Name:       c.Name,
				Location:   configLocation(c),
				Summary:    c.Summary,
				Type:       flagDisplayType(c.Schema),
				Required:   c.Schema != nil && c.Schema.Required,
				Default:    schemaDefaultString(c.Schema),
				Enum:       enumOf(c.Schema),
				Deprecated: c.Deprecated,
			})
		}
	}
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children))
	return d
}

// envVarLabel is the environment variable an env input reads: its explicit
// schema.variable, else the snake-upper form of its logical name (mirroring the
// binder's default key→env-var derivation, e.g. "apiKey" → "API_KEY").
func envVarLabel(e EnvInput) string {
	if e.Schema != nil && e.Schema.Variable != "" {
		return e.Schema.Variable
	}
	return snakeUpper(e.Name)
}

// configLocation is where a config input is read from, for display: "<file>.<key>"
// when a source file is named, the bare key when an explicit key differs from the
// logical name, or "" when the input reads from its own name (nothing to add).
func configLocation(c ConfigInput) string {
	if c.Schema == nil {
		return ""
	}
	key := c.Schema.Key
	switch {
	case c.Schema.File != "":
		if key == "" {
			key = c.Name
		}
		return c.Schema.File + "." + key
	case key != "":
		return key
	default:
		return ""
	}
}

// groupCommands buckets command rows by their Group, preserving the order in which each
// group first appears in the declared command list. Ungrouped rows (Group == "") form a
// bucket with Title "" — each template heads it with its own default. When no row
// declares a group, the result is a single Title-"" bucket holding every command in
// order, so a non-grouped command list renders byte-identically to before.
func groupCommands(rows []helpCmdRow) []helpCmdGroup {
	if len(rows) == 0 {
		return nil
	}
	idx := map[string]int{}
	var groups []helpCmdGroup
	for _, r := range rows {
		i, ok := idx[r.Group]
		if !ok {
			i = len(groups)
			idx[r.Group] = i
			groups = append(groups, helpCmdGroup{Title: r.Group})
		}
		groups[i].Commands = append(groups[i].Commands, r)
	}
	return groups
}

// snakeUpper converts a logical name to the conventional SCREAMING_SNAKE_CASE env-var
// form: word boundaries are '-'/'_'/' ' and lower→upper case transitions.
func snakeUpper(name string) string {
	var b strings.Builder
	var prev rune
	for i, r := range name {
		switch {
		case r == '-' || r == '_' || r == ' ':
			b.WriteByte('_')
		case i > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			b.WriteByte('_')
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
		prev = r
	}
	return b.String()
}

// flagRow builds the help-row for a single flag (shared by a command's own Flags
// section and the Cascading section it contributes to its descendants).
func flagRow(f FlagInput) helpFlagRow {
	return helpFlagRow{
		Identifiers: flagIdentifiers(f),
		Summary:     f.Summary,
		Type:        flagDisplayType(f.Schema),
		Required:    f.Schema != nil && f.Schema.Required,
		Default:     schemaDefaultString(f.Schema),
		Enum:        enumOf(f.Schema),
		Deprecated:  f.Deprecated,
	}
}

// cascadingFlagsOf returns the help-rows for a command's own flags marked
// cascading: true (and not hidden) — the flags it advertises on its descendants'
// pages. Order follows declaration order, matching the Flags section.
func cascadingFlagsOf(inputs *Inputs) []helpFlagRow {
	if inputs == nil {
		return nil
	}
	var rows []helpFlagRow
	for _, f := range inputs.Flags {
		if f.Hidden || !f.Cascading {
			continue
		}
		rows = append(rows, flagRow(f))
	}
	return rows
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

// buildFeatureFramework turns a feature's nodes into the embed vars + resolver
// cases the framework template emits (one resolver per feature).
func buildFeatureFramework(nodes []helpNode, dir string, feat docFeature) *helpFramework {
	h := &helpFramework{Resolver: feat.resolver, Noun: feat.noun, PerShell: feat.perShell}
	for _, hn := range nodes {
		name := feat.varPrefix + hn.prefix
		h.Vars = append(h.Vars, helpVar{Name: name, Embed: dir + "/" + hn.file})
		quoted := make([]string, len(hn.paths))
		for i, p := range hn.paths {
			quoted[i] = strconv.Quote(p)
		}
		h.Cases = append(h.Cases, helpCase{PathsLiteral: strings.Join(quoted, ", "), Var: name})
	}
	return h
}

// writeFeatureFiles produces each command's rendered page for one feature under
// its dir in the framework package. When the command's verbatim spec string for
// the feature is set, that string is written byte-exact; otherwise the page is
// rendered from the shared doc-data via the feature's template. Either way the
// file is rotini-managed — (re)written every pass, skipped when already identical —
// like rotini.go and handlers.go. The template is loaded (seeding the editable
// default when missing) only when at least one command renders.
func writeFeatureFiles(featDir string, nodes []helpNode, feat docFeature) error {
	if featDir == "" {
		return fmt.Errorf("generate.features.%s.dir must not be empty", feat.name)
	}
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		return fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
	}

	renders := false
	for _, hn := range nodes {
		if hn.verbatim == "" {
			renders = true
			break
		}
	}

	var tmpl *template.Template
	if renders {
		t, err := loadFeatureTemplate(featDir, feat)
		if err != nil {
			return err
		}
		tmpl = t
	}

	for _, hn := range nodes {
		path := filepath.Join(featDir, hn.file)
		if hn.verbatim != "" {
			// Verbatim: write exactly what the spec supplied — byte-for-byte, no
			// trailing-newline normalization (the author controls it via YAML).
			if err := writeIfChanged(path, hn.verbatim); err != nil {
				return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
			}
			continue
		}
		rendered, err := renderHelpText(tmpl, hn.data)
		if err != nil {
			return fmt.Errorf("render %s for %q: %w", feat.name, hn.name, err)
		}
		if err := writeIfChanged(path, rendered); err != nil {
			return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
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

// writeFileBytes writes content to path atomically (temp file then rename, so an
// interrupted generate never leaves a torn, half-written file) and creates the parent
// directory if needed — matching how the generated Go file is written (see format.go). It
// backs every non-Go output: help/man/markdown pages, completion scripts, and handler stubs.
func writeFileBytes(path, content string) error {
	if err := fs.WriteFile(path, []byte(content), fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// loadFeatureTemplate reads the feature dir's editable template, seeding it from
// the embedded default when missing, and parses it with the shared FuncMap.
func loadFeatureTemplate(featDir string, feat docFeature) (*template.Template, error) {
	path := filepath.Join(featDir, feat.tmplFile)
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		def, derr := templateFS.ReadFile(feat.embedTmpl)
		if derr != nil {
			return nil, fmt.Errorf("read embedded default %s template: %w", feat.name, derr)
		}
		if werr := os.WriteFile(path, def, 0o644); werr != nil {
			return nil, fmt.Errorf("seed %s template %s: %w", feat.name, path, werr)
		}
		src = def
	} else if err != nil {
		return nil, fmt.Errorf("read %s template %s: %w", feat.name, path, err)
	}
	tmpl, err := template.New(feat.tmplFile).Funcs(helpFuncMap()).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse %s template %s: %w", feat.name, path, err)
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
	d.CommandGroups = append([]helpCmdGroup(nil), d.CommandGroups...)
	for i := range d.CommandGroups {
		d.CommandGroups[i].Commands = append([]helpCmdRow(nil), d.CommandGroups[i].Commands...)
		for j := range d.CommandGroups[i].Commands {
			d.CommandGroups[i].Commands[j].Summary = clean(d.CommandGroups[i].Commands[j].Summary)
			d.CommandGroups[i].Commands[j].Deprecated = clean(d.CommandGroups[i].Commands[j].Deprecated)
		}
	}
	d.Arguments = append([]helpArgRow(nil), d.Arguments...)
	for i := range d.Arguments {
		d.Arguments[i].Summary = clean(d.Arguments[i].Summary)
		d.Arguments[i].Deprecated = clean(d.Arguments[i].Deprecated)
	}
	cleanFlags := func(rows []helpFlagRow) []helpFlagRow {
		rows = append([]helpFlagRow(nil), rows...)
		for i := range rows {
			rows[i].Summary = clean(rows[i].Summary)
			rows[i].Deprecated = clean(rows[i].Deprecated)
		}
		return rows
	}
	d.Flags = cleanFlags(d.Flags)
	d.Cascading = cleanFlags(d.Cascading)
	d.Environment = append([]helpEnvRow(nil), d.Environment...)
	for i := range d.Environment {
		d.Environment[i].Summary = clean(d.Environment[i].Summary)
		d.Environment[i].Deprecated = clean(d.Environment[i].Deprecated)
	}
	d.Configuration = append([]helpConfigRow(nil), d.Configuration...)
	for i := range d.Configuration {
		d.Configuration[i].Summary = clean(d.Configuration[i].Summary)
		d.Configuration[i].Deprecated = clean(d.Configuration[i].Deprecated)
	}
	d.ExitStatus = append([]helpExitRow(nil), d.ExitStatus...)
	for i := range d.ExitStatus {
		d.ExitStatus[i].Summary = clean(d.ExitStatus[i].Summary)
	}
	d.SeeAlso = append([]string(nil), d.SeeAlso...)
	for i := range d.SeeAlso {
		d.SeeAlso[i] = clean(d.SeeAlso[i])
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
