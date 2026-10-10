package codegen

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// This file builds help topics: the pages `topics:` declares on the root, which aren't commands.
// Each topic is one more node in the help, man and markdown features, so `Help("<topic>")`,
// ManPages and MarkdownPages serve it like a command's page.

// topicGenerateEnvironment is the `generate:` value that builds a topic listing every
// environment variable the program reads.
const topicGenerateEnvironment = "environment"

// topicNodes returns one node per declared topic for a doc feature, after the commands' nodes.
// Pages show the root's display name; files use its real name (help_demo.topic.filters.txt,
// demo-filters.1, markdown_demo.topic.filters.md).
func topicNodes(gp *program, feat docFeature, section, date string) []helpNode {
	topics := gp.rootHelp.Topics
	if len(topics) == 0 {
		return nil
	}
	headings := resolveHeadings(gp.rootHelp)
	out := make([]helpNode, 0, len(topics))
	for _, t := range topics {
		file := feat.filePrefix + gp.rootName + ".topic." + t.Name + feat.ext
		if feat.manPages {
			file = manPageName(gp.rootName, []string{t.Name}) + "." + section
		}
		data := templateHelpData{
			Topic:        true,
			Invocation:   gp.rootDisplay + " help " + t.Name,
			Headings:     headings,
			Summary:      t.Summary,
			Description:  t.Body,
			PageName:     manPageName(gp.rootName, []string{t.Name}),
			Section:      section,
			Source:       gp.rootDisplay,
			Date:         date,
			RelatedPages: []string{manPageName(gp.rootName, nil)},
		}
		if t.Generate == topicGenerateEnvironment {
			data.Environment = environmentTopicRows(gp)
		}
		out = append(out, helpNode{
			prefix: "Topic" + gp.rootPascal + toPascalCase(t.Name),
			file:   file,
			paths:  []string{t.Name},
			name:   gp.rootDisplay + " help " + t.Name,
			data:   data,
			path:   []string{t.Name},
			listed: true,
			topic:  true,
		})
	}
	return out
}

// topicRows returns the root page's list of topics, in declared order.
func topicRows(topics []Topic) []templateDocTopicRow {
	rows := make([]templateDocTopicRow, 0, len(topics))
	for _, t := range topics {
		rows = append(rows, templateDocTopicRow{Name: t.Name, Summary: t.Summary})
	}
	return rows
}

// topicPageNames returns the man page names of the root's topics, for the root page's
// cross-references.
func topicPageNames(gp *program) []string {
	out := make([]string, 0, len(gp.rootHelp.Topics))
	for _, t := range gp.rootHelp.Topics {
		out = append(out, manPageName(gp.rootName, []string{t.Name}))
	}
	return out
}

// environmentTopicRows lists every environment variable the program reads, one row per
// variable sorted by name: env inputs, flag and argument fallbacks, variable_file names, the
// completion switches, and the XDG variables config discovery reads. A row names the commands
// that declare it. Hidden commands and hidden inputs are left out.
func environmentTopicRows(gp *program) []templateDocEnvRow {
	t := envTopic{rows: map[string]*templateDocEnvRow{}, prefix: gp.envPrefix}
	t.inputs(gp.rootDisplay, gp.rootInputs)
	var walk func(nodes []rnode, names []string)
	walk = func(nodes []rnode, names []string) {
		for _, n := range nodes {
			if n.hidden {
				continue
			}
			path := append(slices.Clone(names), n.name)
			t.inputs(gp.rootDisplay+" "+strings.Join(path, " "), n.inputs)
			walk(n.children, path)
		}
	}
	walk(gp.tree, nil)
	for _, r := range completionEnvRows(gp.conf) {
		t.add(r.Var, r, "")
	}
	for _, cf := range gp.configFiles {
		if cf.Discover == nil {
			continue
		}
		switch cf.Discover.Strategy {
		case "xdg", "native":
			t.add("XDG_CONFIG_HOME", templateDocEnvRow{Summary: "the directory configuration files are found in (default ~/.config)"}, "")
		case "xdg-system":
			t.add("XDG_CONFIG_DIRS", templateDocEnvRow{Summary: "the system directories configuration files are found in (default /etc/xdg)"}, "")
		}
	}
	out := make([]templateDocEnvRow, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b templateDocEnvRow) int { return cmp.Compare(a.Var, b.Var) })
	return out
}

// envTopic collects the environment topic's rows, one per variable.
type envTopic struct {
	rows   map[string]*templateDocEnvRow
	prefix string // the program's env_prefix, for derived variable names
}

// add records that command reads variable, described by row the first time the variable is seen.
func (t envTopic) add(variable string, row templateDocEnvRow, command string) {
	variable = strings.TrimSpace(variable)
	if variable == "" {
		return
	}
	r, ok := t.rows[variable]
	if !ok {
		row.Var = variable
		r = &row
		t.rows[variable] = r
	}
	if command != "" && !slices.Contains(r.Commands, command) {
		r.Commands = append(r.Commands, command)
	}
}

// addNames records the variables one input reads, in lookup order: each name after the first
// notes the first as the name it shares the input with.
func (t envTopic) addNames(names []string, row templateDocEnvRow, command string) {
	first := ""
	for _, v := range names {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		r := row
		r.AliasOf = first
		t.add(v, r, command)
		if first == "" {
			first = v
		}
	}
}

// fileRow describes a variable that names a file holding an input's value.
func fileRow(summary string) templateDocEnvRow {
	if summary == "" {
		return templateDocEnvRow{Summary: "names a file holding the value"}
	}
	return templateDocEnvRow{Summary: "names a file holding: " + summary}
}

// envRow describes a variable an input reads.
func envRow(summary string, schema *InputSchema, deprecated, since, removedIn, stability string) templateDocEnvRow {
	return templateDocEnvRow{
		Summary: summary, Type: flagDisplayType(schema), Default: schemaDefaultString(schema), Secret: schema != nil && schema.Secret,
		Deprecated: deprecated, DeprecatedSince: since, RemovedIn: removedIn, Stability: stability,
	}
}

// inputs records the variables one command's visible inputs read: its env inputs and their
// variable files, and its flags' and arguments' environment fallbacks.
func (t envTopic) inputs(command string, in *Inputs) {
	if in == nil {
		return
	}
	for _, e := range in.Env {
		if e.Hidden {
			continue
		}
		row := envRow(e.Summary, e.Schema, e.Deprecated, e.DeprecatedSince, e.RemovedIn, e.Stability)
		if e.Schema != nil {
			row.Nesting = e.Schema.Nesting
		}
		t.addNames(strings.Split(envVarName(e, t.prefix), ","), row, command)
		if e.Schema != nil && e.Schema.VariableFile != "" {
			t.add(e.Schema.VariableFile, fileRow(e.Summary), command)
		}
	}
	for _, f := range in.Flags {
		if f.Hidden {
			continue
		}
		row := envRow(f.Summary, f.Schema, f.Deprecated, f.DeprecatedSince, f.RemovedIn, f.Stability)
		t.addNames(strings.Split(flagEnvVar(f.Schema, flagReconKey(f.Name, f.Schema), t.prefix), ","), row, command)
		if f.Schema != nil && f.Schema.VariableFile != "" {
			t.add(f.Schema.VariableFile, fileRow(f.Summary), command)
		}
	}
	for _, a := range in.Arguments {
		if a.Hidden {
			continue
		}
		env, _ := argumentFallback(a, t.prefix, false)
		row := envRow(a.Summary, a.Schema, a.Deprecated, a.DeprecatedSince, a.RemovedIn, a.Stability)
		t.addNames(env, row, command)
	}
}

// topicsLiteral renders the Definition's Topics field, the topic names and summaries completion
// offers after `help`, or "" when the root declares none.
func topicsLiteral(gp *program) string {
	if len(gp.rootHelp.Topics) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Topics: []%s.TopicDef{\n", rotiniPkgName)
	for _, t := range gp.rootHelp.Topics {
		fmt.Fprintf(&b, "{Name: %q, Summary: %q},\n", t.Name, t.Summary)
	}
	b.WriteString("},\n")
	return b.String()
}

// contractTopic is one help topic in the contract document.
type contractTopic struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Body     string `json:"body,omitempty"`
	Generate string `json:"generate,omitempty"`
}

// contractTopics returns the root's help topics for the contract, or nil when it declares none.
func contractTopics(topics []Topic) []contractTopic {
	if len(topics) == 0 {
		return nil
	}
	out := make([]contractTopic, 0, len(topics))
	for _, t := range topics {
		out = append(out, contractTopic{Name: t.Name, Summary: t.Summary, Body: t.Body, Generate: t.Generate})
	}
	return out
}
