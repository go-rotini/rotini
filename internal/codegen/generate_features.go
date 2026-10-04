package codegen

import (
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/go-rotini/rotini"
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

// docFeature describes one doc-rendered codegen feature. All share the doc-data pipeline and
// differ only in file suffix, embed-var and resolver names, the editable template, and which
// per-command verbatim spec string escapes the render.
//
// Enabled features default to one shared embed dir, so their output files must be
// distinguishable by name alone: filePrefix is feature-unique and pruning considers only files
// matching both it and ext, so features sharing a dir can never prune each other's files.
type docFeature struct {
	name       string               // feature key, e.g. "help"
	noun       string               // word used in the resolver doc comment / error, e.g. "help"
	varPrefix  string               // embed-var prefix, e.g. "Help" → HelpRotiniGenerate
	resolver   string               // resolver func name, e.g. "Help"
	ext        string               // output file suffix, e.g. ".txt"
	filePrefix string               // feature-unique output file prefix, e.g. "help_"
	tmplFile   string               // editable template file name in the feature dir ("" = none)
	embedded   string               // embedded default template text, from generate_renderer.go ("" = none)
	verbatim   func(cmdHelp) string // the per-command verbatim escape for this feature (nil = none)
	perShell   bool                 // completion: keyed by shell name, not command path
	strip      bool                 // strip spec-authored ANSI from the output (man/markdown — never help)
	// manPages: files are named as man pages, <page-name>.<section> (taskr-add.1), rather than
	// <filePrefix><path><ext>, and pages render as roff through renderManText.
	manPages bool
	// pagesFunc names the generated function listing every visible command's page
	// (ManPages, MarkdownPages); "" for features that do not get one.
	pagesFunc string
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
		tmplFile: manTemplateName, embedded: templateMan,
		verbatim:  func(h cmdHelp) string { return h.Man },
		strip:     true, // a roff man page carries no legitimate SGR
		manPages:  true,
		pagesFunc: "ManPages",
	}
	markdownFeatureDesc = docFeature{
		name: "markdown", noun: "markdown", varPrefix: "Markdown", resolver: "Markdown",
		ext: ".md", filePrefix: "markdown_", tmplFile: markdownTemplateName, embedded: templateMarkdown,
		verbatim:  func(h cmdHelp) string { return h.Markdown },
		strip:     true, // a markdown file carries no legitimate SGR
		pagesFunc: "MarkdownPages",
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

// helpNode is one command's help wiring: the embed var and resolver identity plus what is
// needed to produce its .txt, either verbatim from the command's `help` string or rendered
// from data through the template.
type helpNode struct {
	prefix   string           // PascalCase command prefix; the embed var is "Help"+prefix
	file     string           // .txt file name within the help dir
	paths    []string         // resolver case values (name/alias permutations); root = [""]
	name     string           // the command's invocation name, e.g. "rotini generate"
	verbatim string           // command.help — the exact page; "" means render from data
	data     templateHelpData // rendering inputs (used when verbatim == "")
	path     []string         // the canonical command path below the root; nil for the root
	listed   bool             // in ManPages/MarkdownPages: neither it nor an ancestor is hidden
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

// flattenFeature produces a node per command for one doc feature across the resolved tree, the
// root first and then every sub-command in tree order. Each node carries its verbatim page,
// when the spec set one, and its built doc-data for when it did not.
//
// What a page SHOWS is the display name (`kubectl ctx use`); what it is stored as stays the
// root's real name (help_kubectl-ctx_use.txt, kubectl-ctx-use.1), since a file name with spaces
// in it helps no one.
func flattenFeature(gp *program, feat docFeature) []helpNode {
	section := manSection(gp.conf)
	date := manDate()
	file := func(names []string) string {
		if feat.manPages {
			return manPageName(gp.rootName, names) + "." + section
		}
		if len(names) == 0 {
			return feat.filePrefix + gp.rootName + feat.ext
		}
		return feat.filePrefix + gp.rootName + "_" + strings.Join(names, "_") + feat.ext
	}
	// withPage fills the man page fields every feature's data carries: the page's own name, and
	// the pages it cross-references — its parent's, then each visible child's.
	withPage := func(d templateHelpData, names []string, children []rnode, output *Schema) templateHelpData {
		d.Output = outputDoc(output, gp.schemas)
		d.PageName = manPageName(gp.rootName, names)
		d.Section = section
		d.Source = gp.rootDisplay
		d.Date = date
		if len(names) > 0 {
			d.RelatedPages = append(d.RelatedPages, manPageName(gp.rootName, names[:len(names)-1]))
		}
		for _, c := range children {
			if !c.hidden {
				d.RelatedPages = append(d.RelatedPages, manPageName(gp.rootName, append(slices.Clone(names), c.name)))
			}
		}
		return d
	}

	out := []helpNode{{
		prefix:   gp.rootPascal,
		file:     file(nil),
		paths:    []string{""},
		name:     gp.rootDisplay,
		verbatim: feat.verbatim(gp.rootHelp),
		data:     withPage(buildHelpData(gp.rootDisplay, gp.rootHelp, gp.rootInputs, gp.tree, gp.rootPlugins, nil, gp.envPrefix), nil, gp.tree, gp.rootOutput),
		listed:   true,
	}}

	// cascading carries the cascading flags accumulated from a node's ancestors
	// (the root's own cascading flags seed the root's children, and so on down).
	var walk func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow, listed bool)
	walk = func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow, listed bool) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootDisplay + " " + strings.Join(childNames, " ")
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     file(childNames),
				paths:    permute(childChain),
				name:     invocation,
				verbatim: feat.verbatim(n.help),
				data:     withPage(buildHelpData(invocation, n.help, n.inputs, n.children, n.plugins, cascading, gp.envPrefix), childNames, n.children, n.output),
				path:     childNames,
				listed:   listed && !n.hidden,
			})
			childCascading := append(append([]templateDocFlagRow{}, cascading...), cascadingFlagsOf(n.inputs)...)
			walk(n.children, childChain, childNames, childCascading, listed && !n.hidden)
		}
	}
	walk(gp.tree, nil, nil, cascadingFlagsOf(gp.rootInputs), true)
	return out
}

// manPageName is a command's man page name: the root's name and the command path joined with
// "-", lowercased — taskr, taskr-add, kubectl-ctx-use. It is the name man looks the page up by,
// and the one used everywhere: the .TH header, the file, and cross-references. display_name
// changes what a page says, not what it is called.
func manPageName(root string, path []string) string {
	return strings.ToLower(strings.Join(append([]string{root}, path...), "-"))
}

// manSection is the man section the conf sets on the man feature, "1" when it sets none.
func manSection(conf *Conf) string {
	if conf != nil && conf.Generate != nil {
		if f := conf.Generate.featureOf("man"); f != nil && f.Section != 0 {
			return strconv.Itoa(f.Section)
		}
	}
	return "1"
}

// manDate is the date a man page's header carries: the day SOURCE_DATE_EPOCH names, in UTC, when
// it is set, and "" otherwise. A page that carried the day it was generated would change on every
// regeneration and break "regenerating changes nothing"; SOURCE_DATE_EPOCH is how a packager
// asks for a fixed, reproducible date.
func manDate() string {
	secs, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")), 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(secs, 0).UTC().Format("2006-01-02")
}

// manPageCollisions reports each pair of commands whose man pages would share one name — which
// happens because a command name may itself contain "-" (`notes tag-remove` and
// `notes tag remove`), or through lowercasing (`Add` and `add`, the same file on a
// case-insensitive file system). Both pages would be written to one file, and one would be lost.
func manPageCollisions(nodes []helpNode) []error {
	first := map[string]string{}
	var problems []error
	for _, n := range nodes {
		page := n.data.PageName
		if prev, ok := first[page]; ok {
			problems = append(problems, fmt.Errorf("commands %q and %q both have the man page name %q; rename one of them", prev, n.name, page))
			continue
		}
		first[page] = n.name
	}
	return problems
}

// completionNodes produces one node per supported shell for the completion
// feature: keyed by shell name (the resolver case), file
// completion_<shell>.txt, embed var Completion<Shell>. No doc-data or
// verbatim — the script comes from completionContents (completionScript per shell).
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
	hd := templateDocHeadings{Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:", Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:", Cascading: "Global Flags:", Examples: "Examples:", Output: "Output:"}
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
	override(&hd.Output, o.Output)
	return hd
}

// buildHelpData assembles the template context for one command from its help
// fields, inputs, direct children, and declared plugins. Hidden
// children/inputs are excluded; plugins join the Commands list (they dispatch
// like any sub-command).
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode, plugins []PluginSpec, ancestorCascading []templateDocFlagRow, envPrefix string) templateHelpData {
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
		d.ExitStatus = append(d.ExitStatus, templateDocExitRow{Code: e.Code, Summary: e.Summary, Output: shapeTypeName(e.Output)})
	}
	var cmds []templateDocCommandRow
	for _, c := range children {
		if c.hidden {
			continue
		}
		// The command's own name is always current (deprecated_identifiers names aliases only),
		// so it goes through with them and comes back off the front.
		names, deprecated := undeprecated(append([]string{c.name}, c.aliases...), c.deprecatedIdentifiers, c.deprecated)
		aliases := names[1:]
		cmds = append(cmds, templateDocCommandRow{
			Name:       c.name,
			Summary:    c.help.Summary,
			Aliases:    aliases,
			Group:      c.group,
			Deprecated: deprecated,
		})
	}
	for _, r := range plugins {
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
	// After the flag rows exist, not before: grouping reads d.Flags.
	d.FlagGroups = groupFlags(d.Flags)
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children) || len(plugins) > 0)
	return d
}

// envVarLabel is the environment variable an env input reads: its explicit
// schema.variable, else the snake-upper form of its logical name (mirroring the
// input reader's default key→env-var derivation, e.g. "apiKey" → "API_KEY").
func envVarLabel(e EnvInput, envPrefix string) string {
	// One derivation, shared with the `env:` tag the input reader pins — see [envVarFor]. Several
	// names read as a list, first preferred.
	return strings.ReplaceAll(envVarName(e, envPrefix), ",", ", ")
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

// groupCommands buckets command rows by Group, preserving the order in which each group first
// appears. Ungrouped rows form a bucket with an empty Title, which each template heads with
// its own default, so a spec that declares no groups renders one bucket of every command.
func groupCommands(rows []templateDocCommandRow) []templateDocCommandGroup {
	var groups []templateDocCommandGroup
	for title, members := range groupByTitle(rows, func(r templateDocCommandRow) string { return r.Group }) {
		groups = append(groups, templateDocCommandGroup{Title: title, Commands: members})
	}
	return groups
}

// groupFlags buckets flag rows by Group, the same way groupCommands buckets commands:
// first-appearance order, and an ungrouped bucket with an empty Title that the template heads
// with its default Flags heading. A command declaring no flag groups therefore renders exactly
// one bucket — identical output to before the key existed.
func groupFlags(rows []templateDocFlagRow) []templateDocFlagGroup {
	var groups []templateDocFlagGroup
	for title, members := range groupByTitle(rows, func(r templateDocFlagRow) string { return r.Group }) {
		groups = append(groups, templateDocFlagGroup{Title: title, Flags: members})
	}
	return groups
}

// groupByTitle buckets rows by the title group returns, yielding each bucket once in the order
// its first row appears.
func groupByTitle[T any](rows []T, group func(T) string) iter.Seq2[string, []T] {
	return func(yield func(string, []T) bool) {
		idx := map[string]int{}
		var titles []string
		var buckets [][]T
		for _, r := range rows {
			t := group(r)
			i, ok := idx[t]
			if !ok {
				i = len(buckets)
				idx[t] = i
				titles = append(titles, t)
				buckets = append(buckets, nil)
			}
			buckets[i] = append(buckets[i], r)
		}
		for i, t := range titles {
			if !yield(t, buckets[i]) {
				return
			}
		}
	}
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

// undeprecated is what a help row shows of the names a command or flag answers to. With
// deprecated_identifiers, the deprecation belongs to THOSE spellings, not to the command or
// flag: the run warns only when one of them is used. So the row leaves them out — they still
// work, and still warn — and carries no deprecation marker, since what it lists is current.
// Only when every name is deprecated does the row list them all and keep the marker. Without
// deprecated_identifiers, the message deprecates the whole thing and is shown as is.
func undeprecated(names, deprecatedIDs []string, message string) ([]string, string) {
	if len(deprecatedIDs) == 0 {
		return names, message
	}
	var kept []string
	for _, n := range names {
		if !slices.Contains(deprecatedIDs, n) {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 && len(names) > 0 {
		return names, message
	}
	return kept, ""
}

// flagRow builds the help-row for a single flag (shared by a command's own Flags
// section and the Cascading section it contributes to its descendants).
func flagRow(f FlagInput) templateDocFlagRow {
	ids, deprecated := undeprecated(flagIdentifiers(f), f.DeprecatedIdentifiers, f.Deprecated)
	row := templateDocFlagRow{
		Identifiers: negatableIdentifiers(f, ids),
		Summary:     f.Summary,
		Group:       f.Group,
		Type:        flagDisplayType(f.Schema),
		Required:    f.Schema != nil && f.Schema.Required,
		Default:     schemaDefaultString(f.Schema),
		Enum:        enumOf(f.Schema),
		Deprecated:  deprecated,
	}
	// An optional value is written attached, so the row says so: `-c, --color[=when]`, with the
	// value token moved inside the brackets on the last identifier.
	if f.Schema != nil && f.Schema.ImplicitValue != nil {
		row.Implicit = defaultString(f.Schema.ImplicitValue)
		if n := len(row.Identifiers); n > 0 && row.Type != "" {
			row.Identifiers = append(slices.Clone(row.Identifiers[:n-1]), row.Identifiers[n-1]+"[="+row.Type+"]")
			row.Type = ""
		}
	}
	return row
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
	if schema != nil && schema.Items != nil && schema.Items.Ref != "" {
		return t // a list of named shapes: []Mount
	}
	if schema != nil && schema.Ref == "" && schema.Type != "" {
		if jsonSchemaTypeToGo(schema.Type) == "[]string" && schema.Items != nil && schema.Items.Ref == "" && schema.Items.Type != "" {
			return "[]" + helpTypeName(schema.Items.Type)
		}
		return helpTypeName(schema.Type)
	}
	return t
}

// valueTypeAliases are the rotini type names that name a KIND of value — a duration, a URL, a
// size — rather than a Go shape. Help shows them as the spec wrote them: `--timeout duration`
// tells the user what to type, where `--timeout time.Duration` tells them how the program
// stores it.
var valueTypeAliases = []string{
	"duration", "time", "datetime", "date",
	"url", "email", "timezone", "mac", "ip", "cidr", "hostport",
	"bytesize", "hexbytes", "base64bytes",
}

// helpTypeName renders a declared type for a help page: value aliases as written, everything
// else resolved to Go, inside list and map spellings too — `[]bytesize`, `map[string]duration`.
func helpTypeName(t string) string {
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "[]" + helpTypeName(elem)
	}
	if key, val, ok := splitMapType(t); ok {
		return "map[" + helpTypeName(key) + "]" + helpTypeName(val)
	}
	if slices.Contains(valueTypeAliases, t) {
		return t
	}
	return jsonSchemaTypeToGo(t)
}

// schemaDefaultString is the default a help page shows: the author's default_text when set,
// else the default itself.
func schemaDefaultString(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if schema.DefaultText != "" {
		return schema.DefaultText
	}
	return defaultString(schema.Default)
}

func enumOf(schema *InputSchema) []string {
	if schema == nil {
		return nil
	}
	return schema.Enum
}

// negatableIdentifiers is how a flag's identifiers read in generated help. A negatable flag
// renders its long forms as "--[no-]color" — one row for the pair, which is what every CLI that
// has the feature does and what makes the negated form discoverable at all. Short forms are
// untouched: they have no negated spelling.
func negatableIdentifiers(f FlagInput, ids []string) []string {
	if f.Schema == nil || !f.Schema.Negatable {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := strings.CutPrefix(id, "--"); ok {
			out = append(out, "--[no-]"+name)
			continue
		}
		out = append(out, id)
	}
	return out
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

// buildFeatureBlock turns a feature's nodes into the embed vars + resolver
// cases the framework template emits (one resolver per feature).
func buildFeatureBlock(nodes []helpNode, dir string, feat docFeature, embed bool, contents []string) templateFeature {
	h := templateFeature{Resolver: feat.resolver, Noun: feat.noun, PerShell: feat.perShell}
	if feat.manPages && len(nodes) > 0 {
		h.Section = nodes[0].data.Section
	}
	if feat.pagesFunc != "" {
		h.PagesFunc = feat.pagesFunc
		for _, hn := range nodes {
			if !hn.listed {
				continue
			}
			path := "nil"
			if len(hn.path) > 0 {
				path = goStringSlice(hn.path)
			}
			h.Pages = append(h.Pages, templateFeaturePage{Name: hn.data.PageName, PathLiteral: path, Var: feat.varPrefix + hn.prefix})
		}
	}
	for i, hn := range nodes {
		name := feat.varPrefix + hn.prefix
		v := templateFeatureVar{Name: name}
		if embed {
			// A "." dir (the feature dir IS the cmd package dir) embeds the bare
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

// docFeatureContents returns each command's page for one feature, in node order. A verbatim spec
// string is used byte-exact; otherwise the page renders from the shared doc-data through the
// feature's template. The template is loaded — seeding the editable default when missing — only
// when at least one command renders; writing the pages is the caller's.
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
		render := renderDocText
		if feat.manPages {
			render = renderManText
		}
		rendered, err := render(tmpl, hn.data)
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
		return fmt.Errorf("generate.features.%s.embed_dir must not be empty", feat.name)
	}
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		return fmt.Errorf("create %s dir %s: %w", feat.name, featDir, err)
	}
	for i, hn := range nodes {
		if err := writeGeneratedFile(filepath.Join(featDir, hn.file), []byte(contents[i])); err != nil {
			return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
		}
	}
	return nil
}

// stripForFeature removes spec-authored ANSI styling from a feature's output
// when the feature is not a terminal surface (man, markdown). Help
// keeps its styling; this returns text unchanged for non-strip features.
func stripForFeature(feat docFeature, text string) string {
	if !feat.strip {
		return text
	}
	return rotini.StripANSI(text)
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
