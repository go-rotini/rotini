package codegen

import (
	"errors"
	"slices"
	"strings"
)

// TreeFn is the signature of [Processor.Tree].
type TreeFn = func(specPath string) (string, error)

// Tree returns the command tree of the spec at specPath ("" finds the .rotini.spec.* in the
// working directory), with composed children resolved: one line per command, indented two
// spaces under its parent, naming the command and its listed aliases, then markers for a
// composed root ($ref), hidden, deprecated and passthrough commands. Declared plugins are
// listed as children, and plugin discovery as a last child line. A spec with errors is
// refused with validate's problems; warnings are ignored.
func (p *Processor) Tree(specPath string) (string, error) {
	rs, err := reconcileSpec(specPath)
	if err != nil {
		return "", p.explainDecodeFailure("spec", err)
	}
	if errs, _ := splitProblems(p.validateAndLintSpec(rs)); len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	// Without a go.mod, local refs still resolve; a mod:// ref fails with its own error.
	_, module, err := findModule()
	if err != nil {
		module = ""
	}
	gp, err := resolveTree(rs.spec, rs.path, module)
	if err != nil {
		return "", err
	}
	root := &rs.spec.Command
	var b strings.Builder
	b.WriteString(root.Name)
	writeTreeMarkers(&b, "", false, "", root.Passthrough)
	b.WriteString("\n")
	writeTreeChildren(&b, 1, gp.tree, root.Plugins, root.PluginDiscovery, root.Name)
	return b.String(), nil
}

// writeTreeChildren writes nodes, then plugins, then the discovery line, at depth.
func writeTreeChildren(b *strings.Builder, depth int, nodes []rnode, plugins []PluginSpec, discovery *PluginDiscovery, host string) {
	indent := strings.Repeat("  ", depth)
	for _, n := range nodes {
		b.WriteString(indent)
		b.WriteString(treeNames(n.name, n.aliases, n.hiddenAliases, n.deprecatedIdentifiers))
		writeTreeMarkers(b, n.ref, n.hidden, n.deprecated, n.passthrough)
		b.WriteString("\n")
		childHost := host
		if n.pluginHost != "" {
			childHost = n.pluginHost
		}
		writeTreeChildren(b, depth+1, n.children, n.plugins, n.discovery, childHost)
	}
	for _, pl := range plugins {
		b.WriteString(indent)
		b.WriteString(treeNames(pl.Name, pl.Aliases, nil, nil))
		b.WriteString("  [plugin]\n")
	}
	if discovery != nil {
		prefix := discovery.Prefix
		if prefix == "" {
			prefix = host + "-"
		}
		b.WriteString(indent)
		b.WriteString("*  [discovers ")
		b.WriteString(prefix)
		b.WriteString("*]\n")
	}
}

// treeNames is a command's name and listed aliases, comma separated, with each deprecated
// alias suffixed "(deprecated alias)". Hidden aliases are never listed.
func treeNames(name string, aliases, hidden, deprecated []string) string {
	names := []string{name}
	for _, a := range aliases {
		switch {
		case slices.Contains(hidden, a):
		case slices.Contains(deprecated, a):
			names = append(names, a+" (deprecated alias)")
		default:
			names = append(names, a)
		}
	}
	return strings.Join(names, ", ")
}

// writeTreeMarkers appends a node's markers, in a fixed order, two spaces after its names.
func writeTreeMarkers(b *strings.Builder, ref string, hidden bool, deprecated string, passthrough bool) {
	var marks []string
	if ref != "" {
		marks = append(marks, "[$ref "+ref+"]")
	}
	if hidden {
		marks = append(marks, "[hidden]")
	}
	if deprecated != "" {
		marks = append(marks, "[deprecated]")
	}
	if passthrough {
		marks = append(marks, "[passthrough]")
	}
	if len(marks) > 0 {
		b.WriteString("  ")
		b.WriteString(strings.Join(marks, " "))
	}
}
