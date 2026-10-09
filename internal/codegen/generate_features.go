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

// Doc features: help, man, markdown and completion page generation.

// The editable doc templates, seeded once into a feature's template_dir when `template: true`.
const (
	helpTemplateName     = "help.txt.tmpl"
	manTemplateName      = "man.txt.tmpl"
	markdownTemplateName = "markdown.md.tmpl"
)

// docFeature describes one generated doc feature. The features share the doc-data pipeline and
// differ in file naming, embed-var and resolver names, template, and which verbatim spec string
// replaces the rendered page.
//
// Features default to one shared embed_dir, so their files must be distinguishable by name:
// each owns a distinct file pattern (see featureOutput.owns), and pruning only touches owned
// files.
type docFeature struct {
	name       string               // feature key, e.g. "help"
	noun       string               // word used in the resolver doc comment and error, e.g. "help"
	varPrefix  string               // embed-var prefix, e.g. "Help" → HelpRotiniGenerate
	resolver   string               // resolver func name, e.g. "Help"
	ext        string               // output file suffix, e.g. ".txt"
	filePrefix string               // feature-unique output file prefix, e.g. "help_"
	tmplFile   string               // editable template file name ("" = none)
	embedded   string               // built-in default template text ("" = none)
	verbatim   func(cmdHelp) string // the per-command verbatim page for this feature (nil = none)
	perShell   bool                 // completion: keyed by shell name, not command path
	strip      bool                 // strip spec-authored ANSI from the output (man, markdown)
	// manPages names files <page-name>.<section> (taskr-add.1) instead of
	// <filePrefix><path><ext>, and renders pages as roff through renderManText.
	manPages bool
	// pagesFunc names the generated function listing every visible command's page
	// (ManPages, MarkdownPages); "" for none.
	pagesFunc string
}

var (
	helpFeatureDesc = docFeature{
		name: "help", noun: "help", varPrefix: "Help", resolver: "Help",
		ext: ".txt", filePrefix: "help_", tmplFile: helpTemplateName, embedded: templateHelp,
		verbatim: func(h cmdHelp) string { return h.Help },
		// Help is a terminal surface, so spec-authored styling is kept.
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
	// completionFeatureDesc is keyed by shell, with no doc-data, template or verbatim page.
	// Scripts come from completionScript.
	completionFeatureDesc = docFeature{
		name: "completion", noun: "completion", varPrefix: "Completion", resolver: "Completion",
		ext: ".txt", filePrefix: "completion_", perShell: true,
	}
)

// completionShells are the shells completionScript supports, in generation order.
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

// helpNode is one command's page for one feature: its embed var and resolver identity plus the
// verbatim page or the data to render it from.
type helpNode struct {
	prefix   string           // PascalCase command prefix; the embed var is varPrefix+prefix
	file     string           // output file name within the embed_dir
	paths    []string         // resolver case values (name/alias permutations); root = [""]
	name     string           // the command's invocation name, e.g. "rotini generate"
	verbatim string           // the feature's verbatim spec page; "" means render from data
	data     templateHelpData // rendering inputs (used when verbatim == "")
	path     []string         // the canonical command path below the root; nil for the root
	listed   bool             // in ManPages/MarkdownPages: neither it nor an ancestor is hidden
}

// cmdHelp bundles a command's doc fields. A non-empty Help, Man or Markdown is that feature's
// exact page; otherwise the page renders from the structured fields.
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
	Groups      []HelpGroup       // command.groups: descriptions and order of its pages' group headings
	Help        string            // verbatim help page (command.help)
	Man         string            // verbatim man page (command.man)
	Markdown    string            // verbatim markdown reference page (command.markdown)
}

// commandHelp gathers the doc fields of a command.
func commandHelp(c Command) cmdHelp {
	return cmdHelp{
		Summary: c.Summary, Description: c.Description, Usage: c.Usage,
		Header: c.Header, Footer: c.Footer, Headings: c.Headings,
		Examples: c.Examples, ExitStatus: c.ExitStatus, SeeAlso: c.SeeAlso, Groups: c.Groups,
		Help: c.Help, Man: c.Man, Markdown: c.Markdown,
	}
}

// flattenFeature returns one node per command for a doc feature, root first, then every
// sub-command in tree order. Pages show the display name (`kubectl ctx use`); files use the
// root's real name (help_kubectl-ctx_use.txt, kubectl-ctx-use.1).
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
	// withPage fills the output and stdin docs and man page fields: the page's name, section,
	// source and date, and its cross-references (the parent's page, then each visible child's).
	withPage := func(d templateHelpData, names []string, children []rnode, output *Schema, inputs *Inputs) templateHelpData {
		d.Output = outputDoc(output, gp.schemas)
		if inputs != nil {
			d.Stdin = stdinDoc(inputs.Stdin, gp.schemas)
		}
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
		data:     withPage(buildHelpData(gp.rootDisplay, gp.rootHelp, gp.rootInputs, gp.tree, gp.rootPlugins, nil, gp.envPrefix, gp.readsConfig(nil)), nil, gp.tree, gp.rootOutput, gp.rootInputs),
		listed:   true,
	}}
	// The variables that switch completion messages and descriptions are inputs the end user
	// sets, so the root's man page lists them with the program's other environment variables.
	if feat.manPages {
		out[0].data.Environment = append(out[0].data.Environment, completionEnvRows(gp.conf)...)
	}

	// cascading accumulates the cascading flags of a node's ancestors.
	var walk func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow, listed bool)
	walk = func(nodes []rnode, identChain [][]string, names []string, cascading []templateDocFlagRow, listed bool) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			invocation := gp.rootDisplay + " " + strings.Join(childNames, " ")
			data := withPage(buildHelpData(invocation, n.help, n.inputs, n.children, n.plugins, cascading, gp.envPrefix, gp.readsConfig(childNames)), childNames, n.children, n.output, n.inputs)
			if n.passthrough {
				data.Passthrough = n.name
			}
			out = append(out, helpNode{
				prefix:   n.prefix,
				file:     file(childNames),
				paths:    permute(childChain),
				name:     invocation,
				verbatim: feat.verbatim(n.help),
				data:     data,
				path:     childNames,
				listed:   listed && !n.hidden,
			})
			childCascading := append(append([]templateDocFlagRow{}, cascading...), cascadingFlagsOf(n.inputs, gp.envPrefix)...)
			walk(n.children, childChain, childNames, childCascading, listed && !n.hidden)
		}
	}
	walk(gp.tree, nil, nil, cascadingFlagsOf(gp.rootInputs, gp.envPrefix), true)
	return out
}

// manPageName returns a command's man page name: the root's name and the command path joined
// with "-", lowercased (taskr, taskr-add, kubectl-ctx-use). It is used for the .TH header, the
// file and cross-references; display_name does not affect it.
func manPageName(root string, path []string) string {
	return strings.ToLower(strings.Join(append([]string{root}, path...), "-"))
}

// manSection returns the man feature's conf section, or "1" when unset.
func manSection(conf *Conf) string {
	if conf != nil && conf.Generate != nil {
		if f := conf.Generate.featureOf("man"); f != nil && f.Section != 0 {
			return strconv.Itoa(f.Section)
		}
	}
	return "1"
}

// manDate returns the man page header date: the UTC day SOURCE_DATE_EPOCH names, or "" when it
// is unset or invalid. The generation date is never used, so regeneration stays byte-stable.
func manDate() string {
	secs, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")), 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(secs, 0).UTC().Format("2006-01-02")
}

// manPageCollisions reports each command whose man page name repeats an earlier one's, as
// happens with a "-" in a command name (`notes tag-remove` and `notes tag remove`) or with
// lowercasing (`Add` and `add`). One page would overwrite the other.
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

// completionNodes returns one completion node per supported shell, keyed by shell name, with
// file completion_<shell>.txt and embed var Completion<Shell>. Scripts come from
// completionContents.
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

// resolveHeadings returns the default section headings, overridden by any set in the spec.
func resolveHeadings(h cmdHelp) templateDocHeadings {
	// Defaults carry the trailing ":" so an override renders verbatim, colon or not.
	hd := templateDocHeadings{Usage: "Usage:", Commands: "Commands:", Arguments: "Arguments:", Flags: "Flags:", Environment: "Environment:", Configuration: "Configuration:", Cascading: "Global Flags:", Examples: "Examples:", Output: "Output:", Stdin: "Stdin:"}
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
	override(&hd.Stdin, o.Stdin)
	return hd
}

// buildHelpData assembles the template data for one command from its doc fields, inputs,
// direct children and plugins. Hidden children and inputs are excluded; plugins are listed
// with the commands. readsConfig says whether the command reads any config file, which decides
// whether its flags show their config keys.
func buildHelpData(invocation string, h cmdHelp, inputs *Inputs, children []rnode, plugins []PluginSpec, ancestorCascading []templateDocFlagRow, envPrefix string, readsConfig bool) templateHelpData {
	d := templateHelpData{
		Invocation:  invocation,
		Headings:    resolveHeadings(h),
		Header:      h.Header,
		Summary:     h.Summary,
		Description: h.Description,
		Usage:       h.Usage,
		Footer:      h.Footer,
		Cascading:   withConfigKeys(ancestorCascading, readsConfig),
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
		// The command name is never deprecated (deprecated_identifiers names aliases only); it
		// rides at the front and is sliced off.
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
	d.CommandGroups = groupCommands(cmds, h.Groups)
	if inputs != nil {
		for _, a := range inputs.Arguments {
			if a.Hidden {
				continue
			}
			constraints, rules := constraintText(a.Schema, "argument")
			d.Arguments = append(d.Arguments, templateDocArgumentRow{
				Name:        a.Name,
				Summary:     a.Summary,
				Required:    a.Schema != nil && a.Schema.Required,
				Variadic:    isVariadicSchema(a.Schema),
				Default:     schemaDefaultString(a.Schema),
				Enum:        enumOf(a.Schema),
				EnumValues:  enumValuesOf(a.Schema),
				Passthrough: a.Passthrough,
				Deprecated:  a.Deprecated,
				Constraints: constraints,
				Rules:       rules,
			})
		}
		for _, f := range inputs.Flags {
			if f.Hidden {
				continue
			}
			d.Flags = append(d.Flags, flagRow(f, envPrefix))
		}
		for _, e := range inputs.Env {
			if e.Hidden {
				continue
			}
			constraints, rules := constraintText(e.Schema, "env")
			d.Environment = append(d.Environment, templateDocEnvRow{
				Var:         envVarLabel(e, envPrefix),
				Summary:     e.Summary,
				Type:        flagDisplayType(e.Schema),
				Required:    e.Schema != nil && e.Schema.Required,
				Default:     schemaDefaultString(e.Schema),
				Enum:        enumOf(e.Schema),
				EnumValues:  enumValuesOf(e.Schema),
				Deprecated:  e.Deprecated,
				Constraints: constraints,
				Rules:       rules,
			})
		}
		for _, c := range inputs.Config {
			if c.Hidden {
				continue
			}
			constraints, rules := constraintText(c.Schema, "config")
			d.Configuration = append(d.Configuration, templateDocConfigRow{
				Name:        c.Name,
				Location:    configLocation(c),
				Summary:     c.Summary,
				Type:        flagDisplayType(c.Schema),
				Required:    c.Schema != nil && c.Schema.Required,
				Default:     schemaDefaultString(c.Schema),
				Enum:        enumOf(c.Schema),
				EnumValues:  enumValuesOf(c.Schema),
				Deprecated:  c.Deprecated,
				Constraints: constraints,
				Rules:       rules,
			})
		}
	}
	d.Flags = withConfigKeys(d.Flags, readsConfig)
	labelFlags(d.Flags, d.Cascading)
	d.FlagGroups = groupFlags(d.Flags, h.Groups)
	d.UsageDerived = deriveUsage(invocation, inputs, hasVisibleChildren(children) || len(plugins) > 0)
	return d
}

// envVarLabel returns the environment variable(s) an env input reads, for display: the names
// envVarName produces (the same ones the generated `env:` tag pins), comma-separated with a
// space, first preferred.
func envVarLabel(e EnvInput, envPrefix string) string {
	return strings.ReplaceAll(envVarName(e, envPrefix), ",", ", ")
}

// configLocation returns where a config input is read from, for display: "<file>.<key>" when a
// source file is named, the explicit key when one is set, else "".
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

// groupCommands buckets command rows by Group (see groupOrder for the order). Ungrouped rows
// form a bucket with an empty Title, which templates head with their default heading. A bucket
// whose group the command's `groups` describes carries that description.
func groupCommands(rows []templateDocCommandRow, declared []HelpGroup) []templateDocCommandGroup {
	var groups []templateDocCommandGroup
	for title, members := range groupByTitle(rows, func(r templateDocCommandRow) string { return r.Group }) {
		groups = append(groups, templateDocCommandGroup{Title: title, Description: groupDescription(declared, title), Commands: members})
	}
	slices.SortStableFunc(groups, func(a, b templateDocCommandGroup) int {
		return groupOrder(declared, a.Title) - groupOrder(declared, b.Title)
	})
	return groups
}

// groupFlags buckets flag rows by Group the same way groupCommands buckets commands.
func groupFlags(rows []templateDocFlagRow, declared []HelpGroup) []templateDocFlagGroup {
	var groups []templateDocFlagGroup
	for title, members := range groupByTitle(rows, func(r templateDocFlagRow) string { return r.Group }) {
		groups = append(groups, templateDocFlagGroup{Title: title, Description: groupDescription(declared, title), Flags: members})
	}
	slices.SortStableFunc(groups, func(a, b templateDocFlagGroup) int {
		return groupOrder(declared, a.Title) - groupOrder(declared, b.Title)
	})
	return groups
}

// groupOrder ranks a bucket for sorting buckets that are already in first-appearance order.
// Without declared groups every bucket ranks the same, so that order stands. With them, the
// ungrouped bucket comes first, then the declared groups in their order, then any others.
func groupOrder(declared []HelpGroup, title string) int {
	if len(declared) == 0 {
		return 0
	}
	if title == "" {
		return -1
	}
	if i := slices.IndexFunc(declared, func(g HelpGroup) bool { return g.Name == title }); i >= 0 {
		return i
	}
	return len(declared)
}

// groupDescription returns the description declared gives the group titled title, or "".
func groupDescription(declared []HelpGroup, title string) string {
	for _, g := range declared {
		if g.Name == title && title != "" {
			return g.Description
		}
	}
	return ""
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

// snakeUpper converts a logical name to SCREAMING_SNAKE_CASE. Word boundaries are '-', '_',
// ' ' and a lower-to-upper (or digit-to-upper) transition.
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

// undeprecated returns the names a help row shows for a command or flag, and its deprecation
// message. With deprecated_identifiers, those spellings are omitted and the row has no marker,
// since the deprecation applies only to them; if every name is deprecated, all are listed with
// the message. Without deprecated_identifiers, names and message pass through unchanged.
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

// flagRow builds the help row for a flag, used by both the Flags and Cascading sections. Its
// env names are the ones the generated field's env tag pins, so help and the runtime agree.
func flagRow(f FlagInput, envPrefix string) templateDocFlagRow {
	ids, deprecated := undeprecated(flagIdentifiers(f), f.DeprecatedIdentifiers, f.Deprecated)
	constraints, rules := constraintText(f.Schema, "flag")
	row := templateDocFlagRow{
		Identifiers: shortFirst(negatableIdentifiers(f, ids)),
		Summary:     f.Summary,
		Group:       f.Group,
		Type:        flagDisplayType(f.Schema),
		Required:    f.Schema != nil && f.Schema.Required,
		Default:     schemaDefaultString(f.Schema),
		Enum:        enumOf(f.Schema),
		EnumValues:  enumValuesOf(f.Schema),
		Deprecated:  deprecated,
		Constraints: constraints,
		Rules:       rules,
		key:         flagReconKey(f.Name, f.Schema),
	}
	// A count flag's rule shows the repetition with its short form: repeat to count: -vvv.
	if f.Schema != nil && f.Schema.Type == "count" {
		if short := shortIdentifier(row.Identifiers); short != "" {
			for i, r := range row.Rules {
				if r == "repeat to count" {
					row.Rules[i] += ": " + short + strings.Repeat(short[1:], 2)
				}
			}
		}
	}
	if env := flagEnvVar(f.Schema, row.key, envPrefix); env != "" {
		row.Env = strings.Split(env, ",")
	}
	// An implicit value must be attached, so the value token moves into brackets on the last
	// identifier: `-c, --color[=when]`.
	if f.Schema != nil && f.Schema.ImplicitValue != nil {
		row.Implicit = defaultString(f.Schema.ImplicitValue)
		if n := len(row.Identifiers); n > 0 && row.Type != "" {
			row.Identifiers = append(slices.Clone(row.Identifiers[:n-1]), row.Identifiers[n-1]+"[="+row.Type+"]")
			row.Type = ""
		}
	}
	return row
}

// shortFirst returns identifiers with the short ones (-v) before the long ones (--verbose),
// each kind in declared order, so every page reads the same way whatever order the spec used.
func shortFirst(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !strings.HasPrefix(id, "--") {
			out = append(out, id)
		}
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "--") {
			out = append(out, id)
		}
	}
	return out
}

// shortIdentifier returns a flag's first short identifier (-v), or "".
func shortIdentifier(ids []string) string {
	for _, id := range ids {
		if !strings.HasPrefix(id, "--") {
			return id
		}
	}
	return ""
}

// longOnlyPad indents a long-only flag's identifiers to the column where long forms start on
// rows with a short one: the width of "-v, ".
const longOnlyPad = "    "

// labelFlags sets the Label of every flag row on one page (its own flags and the cascading
// flags it inherits). When any row has a short identifier, a row without one is indented so
// long forms line up in one column.
func labelFlags(pages ...[]templateDocFlagRow) {
	anyShort := false
	for _, rows := range pages {
		for _, r := range rows {
			if shortIdentifier(r.Identifiers) != "" {
				anyShort = true
			}
		}
	}
	for _, rows := range pages {
		for i := range rows {
			rows[i].Label = strings.Join(rows[i].Identifiers, ", ")
			if anyShort && shortIdentifier(rows[i].Identifiers) == "" {
				rows[i].Label = longOnlyPad + rows[i].Label
			}
		}
	}
}

// cascadingFlagsOf returns help rows for a command's visible cascading flags, in declaration
// order, for its descendants' pages.
func cascadingFlagsOf(inputs *Inputs, envPrefix string) []templateDocFlagRow {
	if inputs == nil {
		return nil
	}
	var rows []templateDocFlagRow
	for _, f := range inputs.Flags {
		if f.Hidden || !f.Cascading {
			continue
		}
		rows = append(rows, flagRow(f, envPrefix))
	}
	return rows
}

// withConfigKeys returns rows with ConfigKey set from each flag's key when the page's command
// reads config files, and cleared when it reads none: a key no file is read for points nowhere.
// A cascading flag is judged by the page it appears on, since the runtime reads the invoked
// command's files.
func withConfigKeys(rows []templateDocFlagRow, readsConfig bool) []templateDocFlagRow {
	if rows == nil {
		return nil
	}
	out := slices.Clone(rows)
	for i := range out {
		out[i].ConfigKey = ""
		if readsConfig {
			out[i].ConfigKey = out[i].key
		}
	}
	return out
}

// readsConfig reports whether the command at path (below the root) reads any config file: one
// declared on it or an ancestor, or an unscoped one, as the input reader's chainConfigFiles
// selects them.
func (gp *program) readsConfig(path []string) bool {
	scope := strings.Join(append([]string{gp.rootName}, path...), "/")
	for _, cf := range gp.configFiles {
		if cf.Scope == "" || cf.Scope == scope || strings.HasPrefix(scope, cf.Scope+"/") {
			return true
		}
	}
	return false
}

// deriveUsage builds the default usage line: the invocation, [flags] when there are visible
// flags, a <command> slot when there are visible children, then each visible argument
// (<required> or [optional], "..." when variadic), with [--] before a passthrough argument. A
// command's flags are accepted anywhere after its name, so before its arguments is always right.
func deriveUsage(invocation string, inputs *Inputs, hasChildren bool) string {
	var b strings.Builder
	b.WriteString(invocation)
	if hasVisibleFlags(inputs) {
		b.WriteString(" [flags]")
	}
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
				name = a.Schema.Placeholder
			}
			if isVariadicSchema(a.Schema) {
				name += "..."
			}
			if a.Passthrough {
				b.WriteString(" [--]")
			}
			if a.Schema != nil && a.Schema.Required {
				b.WriteString(" <")
				b.WriteString(name)
				b.WriteString(">")
			} else {
				b.WriteString(" [")
				b.WriteString(name)
				b.WriteString("]")
			}
		}
	}
	return b.String()
}

func isVariadicSchema(schema *InputSchema) bool {
	return strings.HasPrefix(getSchemaType(schema), "[]")
}

// flagDisplayType returns the value token shown after a flag's identifiers in docs: the
// placeholder when set, else the display type name; "" for bool and count flags, which take no
// value.
func flagDisplayType(schema *InputSchema) string {
	t := getSchemaType(schema)
	if t == "bool" || (schema != nil && schema.Type == "count") {
		return ""
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

// valueTypeAliases are the rotini type names that name a kind of value (a duration, a URL, a
// size) rather than a Go type. Docs show them as written: `--timeout duration`, not
// `--timeout time.Duration`.
var valueTypeAliases = []string{
	"duration", "time", "datetime", "date",
	"url", "email", "timezone", "mac", "ip", "cidr", "hostport",
	"bytesize", "hexbytes", "base64bytes",
}

// helpTypeName renders a declared type for docs: value aliases as written, everything else
// resolved to Go, recursing into list and map types (`[]bytesize`, `map[string]duration`).
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

// schemaDefaultString returns the default docs show: default_text when set, else the default.
func schemaDefaultString(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if schema.DefaultText != "" {
		return schema.DefaultText
	}
	if schema.Secret {
		// A page is shared and pasted; only an author's explicit default_text shows.
		return ""
	}
	return defaultString(schema.Default)
}

func enumOf(schema *InputSchema) []string {
	if schema == nil {
		return nil
	}
	return enumStrings(schema.Enum)
}

// enumValuesOf returns every enum value in declared order with its summary, for the docs pages;
// nil when no value has a summary, so the pages keep the compact value list.
func enumValuesOf(schema *InputSchema) []enumValue {
	if schema == nil || !enumDescribed(schema.Enum) {
		return nil
	}
	return enumValues(schema.Enum)
}

// negatableIdentifiers returns a flag's identifiers as docs show them. A negatable flag's long
// forms render as "--[no-]color"; short forms have no negated spelling and are unchanged.
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

// permute returns every space-joined path through the chain of per-segment identifier sets
// (name and aliases), so the resolver matches aliased paths. An empty chain yields [""].
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

// buildFeatureBlock turns a feature's nodes into the template data for its vars, resolver
// cases and optional pages list. In embed mode each var is a //go:embed path; otherwise it is
// an inline string literal from contents.
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
			// "./x" is not a valid //go:embed pattern, so a "." dir embeds the bare name.
			embedPath := hn.file
			if dir != "" && dir != "." {
				embedPath = dir + "/" + hn.file
			}
			v.Embed = embedPath
		} else {
			// strconv.Quote, not a raw string: content may hold backticks (markdown) and
			// ESC bytes (styled help).
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

// docFeatureContents returns each node's page for one feature, in node order. A verbatim spec
// page is used as is (ANSI-stripped for strip features); otherwise the page renders through
// the feature's template. The template is loaded only when some node renders: from template_dir
// (seeding it when missing) when seedTemplate is set, else from the built-in default.
func docFeatureContents(pl *planner, featDir string, nodes []helpNode, feat docFeature, seedTemplate bool) ([]string, error) {
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
			tmpl, err = loadFeatureTemplate(pl, featDir, feat)
		} else {
			tmpl, err = parseDocTemplate(feat.tmplFile, feat.embedded)
		}
		if err != nil {
			return nil, err
		}
	}

	contents := make([]string, len(nodes))
	for i, hn := range nodes {
		if hn.verbatim != "" {
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

// completionContents returns each node's shell completion script, parallel to nodes.
func completionContents(prog string, envs completionEnvs, nodes []helpNode) ([]string, error) {
	contents := make([]string, len(nodes))
	for i, n := range nodes {
		script, err := completionScript(prog, n.name, envs)
		if err != nil {
			return nil, fmt.Errorf("generate %s completion: %w", n.name, err)
		}
		contents[i] = script
	}
	return contents, nil
}

// writeFeatureOutputs writes each node's content to its file under featDir. Only embed-mode
// features write files; inline content lives in the generated Go source.
func writeFeatureOutputs(pl *planner, featDir string, nodes []helpNode, contents []string, feat docFeature) error {
	if featDir == "" {
		return fmt.Errorf("generate.features.%s.embed_dir must not be empty", feat.name)
	}
	for i, hn := range nodes {
		if err := pl.write(filepath.Join(featDir, hn.file), []byte(contents[i])); err != nil {
			return fmt.Errorf("write %s %s: %w", feat.name, hn.file, err)
		}
	}
	return nil
}

// stripForFeature removes ANSI styling from text for strip features (man, markdown) and
// returns it unchanged otherwise.
func stripForFeature(feat docFeature, text string) string {
	if !feat.strip {
		return text
	}
	return rotini.StripANSI(text)
}

// loadFeatureTemplate reads the editable template in featDir, seeding it from the built-in
// default when missing, and parses it with the shared FuncMap.
func loadFeatureTemplate(pl *planner, featDir string, feat docFeature) (*template.Template, error) {
	path := filepath.Join(featDir, feat.tmplFile)
	src, _, exists, err := pl.read(path)
	if err != nil {
		return nil, fmt.Errorf("read %s template %s: %w", feat.name, path, err)
	}
	if !exists {
		if werr := pl.createOnce(path, []byte(feat.embedded)); werr != nil {
			return nil, fmt.Errorf("seed %s template %s: %w", feat.name, path, werr)
		}
		src = []byte(feat.embedded)
	}
	tmpl, err := parseDocTemplate(feat.tmplFile, string(src))
	if err != nil {
		return nil, fmt.Errorf("%s template %s: %w", feat.name, path, err)
	}
	return tmpl, nil
}
