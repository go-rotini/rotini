package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Usage lines as data: each command's line is carried in the Definition (rtx.Usage) and
// returned by the generated Usage function, whether or not the help feature is on.

// usageLine is a command's usage line: its declared `usage`, else the line derived from its
// shape. It is the line help prints, and holds even when the command has a verbatim help page.
func usageLine(invocation string, h cmdHelp, inputs *Inputs, children []rnode, plugins []PluginSpec) string {
	if h.Usage != "" {
		return h.Usage
	}
	return deriveUsage(invocation, inputs, hasVisibleChildren(children) || len(plugins) > 0)
}

// rootUsage is the root command's usage line.
func rootUsage(gp *program) string {
	return usageLine(gp.rootDisplay, gp.rootHelp, gp.rootInputs, gp.tree, gp.rootPlugins)
}

// usageInvocation is node n's invocation below its parent's.
func usageInvocation(parent string, n rnode) string { return parent + " " + n.name }

// nodeUsage is node n's usage line below its parent's invocation.
func nodeUsage(parent string, n rnode) string {
	return usageLine(usageInvocation(parent, n), n.help, n.inputs, n.children, n.plugins)
}

// usageFuncDecl renders the generated Usage function: one case per command, hidden ones
// included, keyed like Help by every name and alias permutation of its path (hidden aliases
// too, as the command line accepts them).
func usageFuncDecl(gp *program) string {
	var b strings.Builder
	b.WriteString("// Usage returns the usage line of the command identified by path (command names or\n")
	b.WriteString("// aliases; no arguments for the root), or an error when path names no command. A handler\n")
	b.WriteString("// uses rtx.Usage() instead, which is right for a composed command too.\n")
	fmt.Fprintf(&b, "func Usage(path ...string) (string, error) {\n\tswitch %s {\n", resolverSwitch(treeHasAliases(gp.tree)))
	fmt.Fprintf(&b, "\tcase \"\":\n\t\treturn %q, nil\n", rootUsage(gp))
	var walk func(nodes []rnode, chain [][]string, invocation string)
	walk = func(nodes []rnode, chain [][]string, invocation string) {
		for _, n := range nodes {
			childChain := append(append([][]string{}, chain...), slices.Concat([]string{n.name}, n.aliases, n.hiddenAliases))
			paths := resolverPaths(gp.tree, childChain)
			quoted := make([]string, len(paths))
			for i, p := range paths {
				quoted[i] = fmt.Sprintf("%q", p)
			}
			fmt.Fprintf(&b, "\tcase %s:\n\t\treturn %q, nil\n", strings.Join(quoted, ", "), nodeUsage(invocation, n))
			walk(n.children, childChain, usageInvocation(invocation, n))
		}
	}
	walk(gp.tree, nil, gp.rootDisplay)
	b.WriteString("\tdefault:\n\t\treturn \"\", fmt.Errorf(\"no usage for command %q\", strings.Join(path, \" \"))\n\t}\n}\n")
	return b.String()
}

// canonicalPathFunc is the generated function that maps a command path's aliases to names.
const canonicalPathFunc = "rotiniCanonicalPath"

// resolverSwitch is the expression a generated path resolver switches on: the joined path, or,
// when the tree has aliases, the joined path with each alias replaced by its command's name,
// so each command needs one case rather than one per spelling of its path.
func resolverSwitch(aliased bool) string {
	if aliased {
		return canonicalPathFunc + "(path)"
	}
	return `strings.Join(path, " ")`
}

// treeHasAliases reports whether any command in the tree has an alias, hidden or not.
func treeHasAliases(nodes []rnode) bool {
	for _, n := range nodes {
		if len(n.aliases) > 0 || len(n.hiddenAliases) > 0 || treeHasAliases(n.children) {
			return true
		}
	}
	return false
}

// canonicalPathDecl renders the function the path resolvers switch on when the tree has
// aliases, and its table: for each command, by its path of names, its sub-commands' aliases
// and the names they stand for. It is "" for a tree without aliases.
func canonicalPathDecl(tree []rnode) string {
	if !treeHasAliases(tree) {
		return ""
	}
	table := map[string]map[string]string{}
	var walk func(nodes []rnode, parent string)
	walk = func(nodes []rnode, parent string) {
		for _, n := range nodes {
			for _, a := range slices.Concat(n.aliases, n.hiddenAliases) {
				if table[parent] == nil {
					table[parent] = map[string]string{}
				}
				table[parent][a] = n.name
			}
			child := n.name
			if parent != "" {
				child = parent + " " + n.name
			}
			walk(n.children, child)
		}
	}
	walk(tree, "")
	var b strings.Builder
	b.WriteString("// " + canonicalPathFunc + " joins path with spaces, replacing each command alias in it with the\n")
	b.WriteString("// command's name.\n")
	b.WriteString("func " + canonicalPathFunc + "(path []string) string {\n")
	b.WriteString("\twords := strings.Split(strings.Join(path, \" \"), \" \")\n")
	b.WriteString("\tfor i, w := range words {\n")
	b.WriteString("\t\tif name, ok := " + canonicalPathFunc + "Aliases[strings.Join(words[:i], \" \")][w]; ok {\n")
	b.WriteString("\t\t\twords[i] = name\n\t\t}\n\t}\n")
	b.WriteString("\treturn strings.Join(words, \" \")\n}\n\n")
	b.WriteString("// " + canonicalPathFunc + "Aliases maps each command's path of names to its sub-commands' aliases\n")
	b.WriteString("// and the names they stand for.\n")
	b.WriteString("var " + canonicalPathFunc + "Aliases = map[string]map[string]string{\n")
	for _, parent := range slices.Sorted(maps.Keys(table)) {
		fmt.Fprintf(&b, "\t%q: {", parent)
		for i, a := range slices.Sorted(maps.Keys(table[parent])) {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %q", a, table[parent][a])
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return b.String()
}
