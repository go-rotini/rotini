package codegen

import (
	"fmt"
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
	b.WriteString("func Usage(path ...string) (string, error) {\n\tswitch strings.Join(path, \" \") {\n")
	fmt.Fprintf(&b, "\tcase \"\":\n\t\treturn %q, nil\n", rootUsage(gp))
	var walk func(nodes []rnode, chain [][]string, invocation string)
	walk = func(nodes []rnode, chain [][]string, invocation string) {
		for _, n := range nodes {
			childChain := append(append([][]string{}, chain...), slices.Concat([]string{n.name}, n.aliases, n.hiddenAliases))
			paths := permute(childChain)
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
