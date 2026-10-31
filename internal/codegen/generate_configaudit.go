package codegen

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strconv"
)

// ─── the config-name audit ──────────────────────────────────────────────────.

// configNames is what the spec declares that the config-writing calls name: each config
// file's keys, and the profiles when every file with profiles lists them all (a selector with
// an enum), since a profile is otherwise the file's own data.
type configNames struct {
	keys     map[string][]string // file -> its keys
	profiles []string            // nil when the names aren't all declared
	consts   map[string]string   // generated name constant -> its value
}

// configNames gathers the program's config names for [undeclaredConfigNames].
func (p *program) configNames() configNames {
	n := configNames{keys: map[string][]string{}, consts: map[string]string{}}
	complete, anyProfiles := true, false
	for _, f := range p.configFiles {
		keys := n.keys[f.Name]
		for _, k := range p.fileKeys(f) {
			if !slices.Contains(keys, k.key) {
				keys = append(keys, k.key)
			}
		}
		n.keys[f.Name] = keys
		if f.Profiles == nil {
			continue
		}
		anyProfiles = true
		sel := findProfileSelector(p.scopeChain(f.Scope), f.Profiles.Select)
		var allowed []string
		if sel.flag != nil && sel.flag.Schema != nil {
			allowed = enumStrings(sel.flag.Schema.Enum)
		}
		if len(allowed) == 0 && sel.env != nil && sel.env.Schema != nil {
			allowed = enumStrings(sel.env.Schema.Enum)
		}
		if len(allowed) == 0 {
			complete = false
		}
		n.profiles = append(n.profiles, allowed...)
	}
	if !complete || !anyProfiles {
		n.profiles = nil
	}
	if groups, err := p.constants(); err == nil {
		for _, g := range groups {
			for _, c := range g.consts {
				if g.names {
					n.consts[c.name] = c.value
				}
			}
		}
	}
	return n
}

// configCallArgs are the config calls the audit checks: the position of the file argument,
// and of the key argument (-1 for none).
func configCallArgs() map[string][2]int {
	return configCallArgsOnce.value(func() map[string][2]int {
		return map[string][2]int{
			"SetConfigValue": {1, 2}, "UnsetConfigValue": {1, 2},
			"ConfigFilePath": {1, -1}, "ConfigProfiles": {1, -1},
		}
	})
}

var configCallArgsOnce lazy[map[string][2]int]

// undeclaredConfigNames warns about a config file, key or profile a handler names, as a string
// literal or a generated name constant, in a call to rotini's SetConfigValue, UnsetConfigValue,
// ConfigFilePath, ConfigProfiles or ToProfile, that the spec doesn't declare: the call fails at
// run time, where generate can see it now. A name computed at run time isn't checked.
func undeclaredConfigNames(fset *token.FileSet, files map[string]*ast.File, names configNames, moduleRoot string) []error {
	var found []auditFinding
	for _, path := range slices.Sorted(maps.Keys(files)) {
		f := files[path]
		pkg := rotiniContextSelector(f)
		if pkg == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != pkg {
				return true
			}
			if msg := names.check(sel.Sel.Name, call.Args); msg != "" {
				at := findingAt(fset, call.Pos(), moduleRoot)
				at.msg = fmt.Sprintf("%s:%d: %s.%s %s", at.file, at.line, pkg, sel.Sel.Name, msg)
				found = append(found, at)
			}
			return true
		})
	}
	return sortedWarnings(found)
}

// check judges a call to rotini's fn with args: what follows "rotini.<fn>" in a warning, or ""
// when it names nothing undeclared.
func (n configNames) check(fn string, args []ast.Expr) string {
	if fn == "ToProfile" {
		if len(args) == 1 && n.profiles != nil {
			if v, ok := n.literal(args[0]); ok && !slices.Contains(n.profiles, v) {
				return fmt.Sprintf("names the profile %q, which no profile selector's enum lists (%s)", v, quotedList(n.profiles))
			}
		}
		return ""
	}
	at, ok := configCallArgs()[fn]
	if !ok || len(args) <= at[0] {
		return ""
	}
	file, ok := n.literal(args[at[0]])
	if !ok {
		return ""
	}
	keys, declared := n.keys[file]
	if !declared {
		return fmt.Sprintf("names the config file %q, which the spec doesn't declare (it declares %s)", file, cmp.Or(quotedList(slices.Sorted(maps.Keys(n.keys))), "none"))
	}
	if at[1] < 0 || len(args) <= at[1] {
		return ""
	}
	if key, ok := n.literal(args[at[1]]); ok && !slices.Contains(keys, key) {
		return fmt.Sprintf("names the key %q, which no input reads from config file %q", key, file)
	}
	return ""
}

// literal reads e as a string literal or a generated name constant.
func (n configNames) literal(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := n.consts[v.Name]
		return s, ok
	}
	return "", false
}
