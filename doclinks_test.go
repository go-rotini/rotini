package rotini

import (
	"go/ast"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A doc comment's [Name] is a link, and godoc renders an unresolvable one as literal brackets
// without warning. TestDocLinksResolve checks every link against the package's declarations.

// docLinkRe matches a godoc symbol link: [Name] or [Type.Method], optionally package-qualified.
var docLinkRe = regexp.MustCompile(`\[((?:\w+\.)?[A-Z]\w*(?:\.\w+)?)\]`)

// illustrativeNames are placeholders a doc comment uses to show a shape rather than to point at
// a symbol, such as a generated inputs type or an example dependency.
var illustrativeNames = map[string]bool{
	"DeployInputs": true, "MigInputs": true,
	"MycliInputs": true, "MycliDeployInputs": true,
	"Store": true, "T": true,
}

func TestDocLinksResolve(t *testing.T) {
	d := packageDoc(t)

	known := map[string]bool{}
	for _, f := range d.Funcs {
		known[f.Name] = true
	}
	for _, ty := range d.Types {
		known[ty.Name] = true
		for _, m := range ty.Methods {
			known[ty.Name+"."+m.Name] = true
		}
		for _, f := range ty.Funcs {
			known[f.Name] = true
		}
		// Exported fields and interface methods are linkable; go/doc does not list interface
		// methods under the type, so they are collected here.
		if ts, ok := ty.Decl.Specs[0].(*ast.TypeSpec); ok {
			switch t := ts.Type.(type) {
			case *ast.StructType:
				for _, fld := range t.Fields.List {
					for _, n := range fld.Names {
						known[ty.Name+"."+n.Name] = true
					}
				}
			case *ast.InterfaceType:
				for _, meth := range t.Methods.List {
					for _, n := range meth.Names {
						known[ty.Name+"."+n.Name] = true
					}
				}
			}
		}
		for _, c := range ty.Consts {
			for _, n := range c.Names {
				known[n] = true
			}
		}
		for _, v := range ty.Vars {
			for _, n := range v.Names {
				known[n] = true
			}
		}
	}
	for _, c := range d.Consts {
		for _, n := range c.Names {
			known[n] = true
		}
	}
	for _, v := range d.Vars {
		for _, n := range v.Names {
			known[n] = true
		}
	}

	bad := map[string][]string{}
	check := func(owner, doc string) {
		for _, line := range strings.Split(doc, "\n") {
			// godoc does not linkify inside an indented code block, and generic syntax
			// (Defaults[MycliInputs]) lives there — scanning it produces only noise.
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") {
				continue
			}
			for _, m := range docLinkRe.FindAllStringSubmatch(line, -1) {
				name := m[1]
				if strings.Contains(name, ".") && !known[name] {
					// A qualified name may belong to another package (errors.As, os.Exit).
					if head := strings.Split(name, ".")[0]; !known[head] {
						continue
					}
				}
				if known[name] || illustrativeNames[name] {
					continue
				}
				bad[name] = append(bad[name], owner)
			}
		}
	}

	check("package", d.Doc)
	for _, f := range d.Funcs {
		check("func "+f.Name, f.Doc)
	}
	for _, ty := range d.Types {
		check("type "+ty.Name, ty.Doc)
		for _, m := range ty.Methods {
			check(ty.Name+"."+m.Name, m.Doc)
		}
		for _, f := range ty.Funcs {
			check("func "+f.Name, f.Doc)
		}
	}

	if len(bad) == 0 {
		return
	}
	names := make([]string, 0, len(bad))
	for n := range bad {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Errorf("doc link [%s] resolves to nothing — godoc will render the brackets literally.\n"+
			"  referenced by: %s\n"+
			"  Fix the name, qualify it ([Context.Get] not [Get]), or add it to illustrativeNames "+
			"if it is a placeholder.", n, strings.Join(bad[n], ", "))
	}
}
