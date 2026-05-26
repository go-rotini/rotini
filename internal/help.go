package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// helpNode is one command's help wiring: the embed var/resolver data plus the
// path to its .txt file. One is produced per command (root + every own and
// composed sub-command). Rotini does not render help — it only seeds a starting
// file and embeds whatever the file holds — so a node carries no descriptions,
// usage, or flags, just identity.
type helpNode struct {
	prefix string   // PascalCase command prefix; the embed var is "Help"+prefix
	file   string   // .txt file name within the help dir
	paths  []string // resolver case values (name/alias permutations); root = [""]
	name   string   // seed contents: the command's invocation name, e.g. "rotini generate"
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

// flattenHelp produces a help node per command for the whole resolved tree: the
// root first, then every sub-command in tree order.
func flattenHelp(gp *genProgram) []helpNode {
	out := []helpNode{{
		prefix: gp.rootPascal,
		file:   gp.rootName + ".txt",
		paths:  []string{""},
		name:   gp.rootName,
	}}

	var walk func(nodes []rnode, identChain [][]string, names []string)
	walk = func(nodes []rnode, identChain [][]string, names []string) {
		for _, n := range nodes {
			seg := append([]string{n.name}, n.aliases...)
			childChain := append(append([][]string{}, identChain...), seg)
			childNames := append(append([]string{}, names...), n.name)
			out = append(out, helpNode{
				prefix: n.prefix,
				file:   gp.rootName + "_" + strings.Join(childNames, "_") + ".txt",
				paths:  permute(childChain),
				name:   gp.rootName + " " + strings.Join(childNames, " "),
			})
			walk(n.children, childChain, childNames)
		}
	}
	walk(gp.tree, nil, nil)
	return out
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

// writeHelpFiles seeds a best-effort help .txt — the command's invocation name —
// for every command that does not already have one under the help dir in the
// framework package. Existing files are never overwritten or removed: each is the
// user's to edit. (Seeding when missing keeps the `//go:embed` directives valid
// even after a file is deleted; orphaned files for removed commands are left in
// place and simply stop being embedded.)
func writeHelpFiles(lay layout, dir string, hnodes []helpNode) error {
	if dir == "" {
		return fmt.Errorf("generate.help.dir must not be empty")
	}
	helpDir := filepath.Join(lay.frameworkDir, filepath.FromSlash(dir))
	if err := os.MkdirAll(helpDir, 0o755); err != nil {
		return fmt.Errorf("create help dir %s: %w", helpDir, err)
	}
	for _, hn := range hnodes {
		path := filepath.Join(helpDir, hn.file)
		if _, err := os.Stat(path); err == nil {
			continue // user-owned; leave it
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat help %s: %w", hn.file, err)
		}
		if err := os.WriteFile(path, []byte(hn.name), 0o644); err != nil {
			return fmt.Errorf("write help %s: %w", hn.file, err)
		}
	}
	return nil
}
