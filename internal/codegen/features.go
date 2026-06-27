package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	rotini "github.com/go-rotini/rotini/internal/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Doc features — help / man / completion page generation.
// ─────────────────────────────────────────────────────────────────────────────.

// helpTemplateName / manTemplateName are the editable, seed-once doc templates
// living in the feature dir (the only user-owned files there). Pruning always
// keeps them.
const (
	helpTemplateName     = "help.txt.tmpl"
	manTemplateName      = "man.txt.tmpl"
	markdownTemplateName = "markdown.md.tmpl"
)

// docFeature describes one doc-rendered codegen feature (help, man). Both share
// the doc-data pipeline (buildHelpData → renderDocText) and differ only in their
// file suffix, embed-var/resolver names, the editable template, and which
// per-command verbatim spec string escapes the render.
//
// All enabled features default to ONE shared embed dir, so each feature's
// output files must be distinguishable by name alone: filePrefix is a
// feature-unique prefix ("help_" / "man_" / "completion_") that also groups
// each feature's files together in directory listings, ext the file suffix,
// and pruning only considers files matching both — features sharing a dir can
// never prune (or collide with) each other's files.
type docFeature struct {
	name       string               // feature key, e.g. "help"
	noun       string               // word used in the resolver doc comment / error, e.g. "help"
	varPrefix  string               // embed-var prefix, e.g. "Help" → HelpRotiniGenerate
	resolver   string               // resolver func name, e.g. "Help"
	ext        string               // output file suffix, e.g. ".txt"
	filePrefix string               // feature-unique output file prefix, e.g. "help_"
	tmplFile   string               // editable template file name in the feature dir ("" = none)
	embedded   string               // embedded default template text, from renderer.go ("" = none)
	verbatim   func(cmdHelp) string // the per-command verbatim escape for this feature (nil = none)
	perShell   bool                 // completion: keyed by shell name, not command path
	strip      bool                 // strip spec-authored ANSI from the output (man/markdown — never help)
}

var (
	helpFeatureDesc = docFeature{
		name: "help", noun: "help", varPrefix: "Help", resolver: "Help",
		ext: ".txt", filePrefix: "help_", tmplFile: helpTemplateName, embedded: templateHelp,
		verbatim: func(h cmdHelp) string { return h.Help },
		// help is the TERMINAL surface — spec-authored styling is kept.
	}
	manFeatureDesc = docFeature{
		name: "man", noun: "man", varPrefix: "Man", resolver: "Man",
		ext: ".txt", filePrefix: "man_", tmplFile: manTemplateName, embedded: templateMan,
		verbatim: func(h cmdHelp) string { return h.Man },
		strip:    true, // a roff/plain man page carries no legitimate SGR (E6-S1)
	}
	markdownFeatureDesc = docFeature{
		name: "markdown", noun: "markdown", varPrefix: "Markdown", resolver: "Markdown",
		ext: ".md", filePrefix: "markdown_", tmplFile: markdownTemplateName, embedded: templateMarkdown,
		verbatim: func(h cmdHelp) string { return h.Markdown },
		strip:    true, // a markdown file carries no legitimate SGR (E6-S1)
	}
	// completionFeatureDesc is the group's exception: keyed by shell, no doc-data,
	// no template, no verbatim. Scripts come from completionScript at codegen.
	completionFeatureDesc = docFeature{
		name: "completion", noun: "completion", varPrefix: "Completion", resolver: "Completion",
		ext: ".txt", filePrefix: "completion_", perShell: true,
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
	prefix   string           // PascalCase command prefix; the embed var is "Help"+prefix
	file     string           // .txt file name within the help dir
	paths    []string         // resolver case values (name/alias permutations); root = [""]
	name     string           // the command's invocation name, e.g. "rotini generate"
	verbatim string           // command.help — the exact page; "" means render from data
	data     templateHelpData // rendering inputs (used when verbatim == "")
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
	Markdown    string            // verbatim markdown reference page (command.markdown)
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
// doc-data (templateHelpData, used when no verbatim page is given). The file
// extension is the feature's; the doc-data is identical across features.
func flattenFeature(gp *genProgram, feat docFeature) []helpNode {
	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     feat.filePrefix + gp.rootName + feat.ext,
		paths:    []string{""},
		name:     gp.rootName,
		verbatim: feat.verbatim(gp.rootHelp),
		data:     buildHelpData(gp.rootName, gp.rootHelp, gp.rootInputs, gp.tree, gp.rootRemotes, nil, gp.envPrefix),
	}}

	// cascading carries the cascading flags accumulated from a node's ancestors
	// (the root's own cascading flags seed the root's children, and so on down).
	var walk func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow)
	walk = func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootName + " " + strings.Join(childNames, " ")
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     feat.filePrefix + gp.rootName + "_" + strings.Join(childNames, "_") + feat.ext,
				paths:    permute(childChain),
				name:     invocation,
				verbatim: feat.verbatim(n.help),
				data:     buildHelpData(invocation, n.help, n.inputs, n.children, n.remotes, cascading, gp.envPrefix),
			})
			childCascading := append(append([]templateDocFlagRow{}, cascading...), cascadingFlagsOf(n.inputs)...)
			walk(n.children, childChain, childNames, childCascading)
		}
	}
	walk(gp.tree, nil, nil, cascadingFlagsOf(gp.rootInputs))
	return out
}

// completionNodes produces one node per supported shell for the completion
// feature: keyed by shell name (the resolver case), file
// completion_<shell>.txt, embed var Completion<Shell>. No doc-data or
// verbatim — the script comes from writeCompletionFiles.
func completionNodes() []helpNode {
	out := make([]helpNode, 0, len(completionShells))
	for _, sh := range completionShells {
		out = append(out, helpNode{
			prefix: toPascalCase(sh),
			file:   completionFeatureDesc.filePrefix + sh + completionFeatureDesc.ext,
			paths:  []string{sh},
			name:   sh,
		})
	}
	return out
}

// resolveHeadings applies the section-heading defaults, overriding with any set
// in the spec.
func resolveHeadings(h cmdHelp) templateDocHeadings {
	// Defaults carry the trailing ":" so an override is rendered verbatim — a spec
	// author can drop or restyle the colon (the template adds nothing).
	hd := templateDocHeadings{Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:", Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:", Cascading: "Global Flags:", Examples: "Examples:"}
	if h.Headings == nil {
		return hd
	}
	override := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	o := h.Headings
	override(&hd.Usage, o.Usage)
	override(&hd.Commands, o.Commands)
	override(&hd.Arguments, o.Arguments)
	override(&hd.Flags, o.Flags)
	override(&hd.Environment, o.Environment)
	override(&hd.Configuration, o.Configuration)
	override(&hd.Cascading, o.Cascading)
	override(&hd.Examples, o.Examples)
	return hd
}

// buildHelpData assembles the template context for one command from its help
// fields, inputs, direct children, and remote sub-commands. Hidden
// children/inputs are excluded; remotes join the Commands list (they dispatch
// like any sub-command).
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode, remotes []RemoteCommandSpec, ancestorCascading []templateDocFlagRow, envPrefix string) templateHelpData {
	d := templateHelpData{
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
		d.ExitStatus = append(d.ExitStatus, templateDocExitRow(e))
	}
	var cmds []templateDocCommandRow
	for _, c := range children {
		if c.hidden {
			continue
		}
		cmds = append(cmds, templateDocCommandRow{
			Name:       c.name,
			Summary:    c.help.Summary,
			Aliases:    c.aliases,
			Group:      c.group,
			Deprecated: c.deprecated,
		})
	}
	for _, r := range remotes {
		cmds = append(cmds, templateDocCommandRow{
			Name:    r.Name,
			Summary: r.Summary,
			Aliases: r.Aliases,
		})
	}
	d.CommandGroups = groupCommands(cmds)
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			d.Arguments = append(d.Arguments, templateDocArgumentRow{
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
			d.Environment = append(d.Environment, templateDocEnvRow{
				Var:        envVarLabel(e, envPrefix),
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
			d.Configuration = append(d.Configuration, templateDocConfigRow{
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
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children) || len(remotes) > 0)
	return d
}

// envVarLabel is the environment variable an env input reads: its explicit
// schema.variable, else the snake-upper form of its logical name (mirroring the
// binder's default key→env-var derivation, e.g. "apiKey" → "API_KEY").
func envVarLabel(e EnvInput, envPrefix string) string {
	if e.Schema != nil && e.Schema.Variable != "" {
		return e.Schema.Variable // explicit: exempt from env_prefix
	}
	if envPrefix != "" {
		return envPrefix + "_" + snakeUpper(e.Name)
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
func groupCommands(rows []templateDocCommandRow) []templateDocCommandGroup {
	if len(rows) == 0 {
		return nil
	}
	idx := map[string]int{}
	var groups []templateDocCommandGroup
	for _, r := range rows {
		i, ok := idx[r.Group]
		if !ok {
			i = len(groups)
			idx[r.Group] = i
			groups = append(groups, templateDocCommandGroup{Title: r.Group})
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
func flagRow(f FlagInput) templateDocFlagRow {
	return templateDocFlagRow{
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
func cascadingFlagsOf(inputs *Inputs) []templateDocFlagRow {
	if inputs == nil {
		return nil
	}
	var rows []templateDocFlagRow
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
			if a.Schema != nil && a.Schema.Placeholder != "" {
				name = a.Schema.Placeholder // the <>/[]/… decoration still applies
			}
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
	return strings.HasPrefix(getSchemaType(schema), "[]")
}

// flagDisplayType returns the value token shown after a flag's identifiers in
// help/man: the declared placeholder when set, else the resolved type; "" for
// bool flags (which take no value).
func flagDisplayType(schema *InputSchema) string {
	t := getSchemaType(schema)
	if t == "bool" || (schema != nil && schema.Type == "count") {
		return "" // presence flags take no value token
	}
	if schema != nil && schema.Placeholder != "" {
		return schema.Placeholder
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
func buildFeatureFramework(nodes []helpNode, dir string, feat docFeature, embed bool, contents []string) templateFeature {
	h := templateFeature{Resolver: feat.resolver, Noun: feat.noun, PerShell: feat.perShell}
	for i, hn := range nodes {
		name := feat.varPrefix + hn.prefix
		v := templateFeatureVar{Name: name}
		if embed {
			// A "." dir (the feature dir IS the cmdgen package dir) embeds the bare
			// file name — "./x" is not a valid //go:embed pattern.
			embedPath := hn.file
			if dir != "" && dir != "." {
				embedPath = dir + "/" + hn.file
			}
			v.Embed = embedPath
		} else {
			// Inline: the content rides as a Go string literal in the .go — no
			// file, no //go:embed. strconv.Quote handles backticks (markdown) and
			// ANSI/ESC bytes (styled help) that a raw-string literal could not.
			v.Literal = strconv.Quote(contents[i])
		}
		h.Vars = append(h.Vars, v)
		quoted := make([]string, len(hn.paths))
		for j, p := range hn.paths {
			quoted[j] = strconv.Quote(p)
		}
		h.Cases = append(h.Cases, templateFeatureCase{PathsLiteral: strings.Join(quoted, ", "), Var: name})
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
func docFeatureContents(featDir string, nodes []helpNode, feat docFeature, seedTemplate bool) ([]string, error) {
	renders := false
	for _, hn := range nodes {
		if hn.verbatim == "" {
			renders = true
			break
		}
	}

	var tmpl *template.Template
	if renders {
		var err error
		if seedTemplate {
			// template:true — seed the editable default to disk (when missing) and
			// render from it, so the author can customize.
			if err = os.MkdirAll(featDir, 0o755); err != nil {
				return nil, fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
			}
			tmpl, err = loadFeatureTemplate(featDir, feat)
		} else {
			// template:false — render from rotini's built-in default in memory; no
			// editable template is written.
			tmpl, err = parseDocTemplate(feat.tmplFile, feat.embedded)
		}
		if err != nil {
			return nil, err
		}
	}

	contents := make([]string, len(nodes))
	for i, hn := range nodes {
		if hn.verbatim != "" {
			// Verbatim: exactly what the spec supplied — byte-for-byte (the author
			// controls trailing newlines via YAML). A strip feature (man/markdown)
			// still removes any ANSI: a verbatim page is no more a terminal surface
			// than a rendered one.
			contents[i] = stripForFeature(feat, hn.verbatim)
			continue
		}
		rendered, err := renderDocText(tmpl, hn.data)
		if err != nil {
			return nil, fmt.Errorf("render %s for %q: %w", feat.name, hn.name, err)
		}
		contents[i] = stripForFeature(feat, rendered)
	}
	return contents, nil
}

// completionContents computes each shell's completion script (no template, no
// ANSI strip — a script is not a styled surface), parallel to nodes.
func completionContents(prog string, nodes []helpNode) ([]string, error) {
	contents := make([]string, len(nodes))
	for i, n := range nodes {
		script, err := completionScript(prog, n.name)
		if err != nil {
			return nil, fmt.Errorf("generate %s completion: %w", n.name, err)
		}
		contents[i] = script
	}
	return contents, nil
}

// writeFeatureOutputs writes each node's precomputed content to its file under
// featDir — used ONLY in embed mode (//go:embed). Inline features write no
// output files: their content lives in the generated .go as a string literal.
func writeFeatureOutputs(featDir string, nodes []helpNode, contents []string, feat docFeature) error {
	if featDir == "" {
		return fmt.Errorf("generate.features.%s.dir must not be empty", feat.name)
	}
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		return fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
	}
	for i, hn := range nodes {
		if err := writeIfChanged(filepath.Join(featDir, hn.file), contents[i]); err != nil {
			return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
		}
	}
	return nil
}

// stripForFeature removes spec-authored ANSI styling from a feature's output
// when the feature is not a terminal surface (man, markdown — E6-S1). Help
// keeps its styling; this returns text unchanged for non-strip features.
func stripForFeature(feat docFeature, text string) string {
	if !feat.strip {
		return text
	}
	return rotini.Strip(text)
}

// loadFeatureTemplate reads the feature dir's editable template, seeding it from
// the embedded default when missing, and parses it with the shared FuncMap.
func loadFeatureTemplate(featDir string, feat docFeature) (*template.Template, error) {
	path := filepath.Join(featDir, feat.tmplFile)
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if werr := os.WriteFile(path, []byte(feat.embedded), 0o644); werr != nil {
			return nil, fmt.Errorf("seed %s template %s: %w", feat.name, path, werr)
		}
		src = []byte(feat.embedded)
	} else if err != nil {
		return nil, fmt.Errorf("read %s template %s: %w", feat.name, path, err)
	}
	tmpl, err := parseDocTemplate(feat.tmplFile, string(src))
	if err != nil {
		return nil, fmt.Errorf("%s template %s: %w", feat.name, path, err)
	}
	return tmpl, nil
}
