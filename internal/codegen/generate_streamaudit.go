package codegen

import (
	"fmt"
	"go/ast"
	"go/token"
)

// unmarkedStreams warns about each handler of a command that declares an `output:` without
// `output_stream: true` and writes it with WriteOutputItem on its *rotini.Context parameter.
// The declaration is what tells readers of the pages and the contract that the command
// streams, and the runtime checks the call against it.
func (p *program) unmarkedStreams(fset *token.FileSet, files map[string]*ast.File) []error {
	unmarked := map[string]string{} // handler type → invocation
	byPrefix := map[string]genCommand{}
	for _, c := range p.ownCommands() {
		byPrefix[c.prefix] = c
	}
	mark := func(prefix string, output *Schema, stream bool) {
		if c, ok := byPrefix[prefix]; ok && c.handler != "" && output != nil && !stream {
			unmarked[c.handler] = c.invocation
		}
	}
	mark(p.rootPascal, p.rootOutput, p.rootStream)
	var walk func(nodes []rnode)
	walk = func(nodes []rnode) {
		for i := range nodes {
			mark(nodes[i].prefix, nodes[i].output, nodes[i].stream)
			walk(nodes[i].children)
		}
	}
	walk(p.tree)
	if len(unmarked) == 0 {
		return nil
	}

	var found []auditFinding
	for _, f := range files {
		ctxType := rotiniContextSelector(f)
		if ctxType == "" {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Body == nil {
				continue
			}
			invocation, audited := unmarked[receiverTypeName(fd.Recv.List[0].Type)]
			if !audited {
				continue
			}
			params := contextParams(fd, ctxType)
			if len(params) == 0 {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "WriteOutputItem" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || !params[x.Name] {
					return true
				}
				at := findingAt(fset, call.Pos(), p.module.root)
				at.msg = fmt.Sprintf("%s:%d: %q writes items with WriteOutputItem; declare output_stream: true on it, or it fails at run time", at.file, at.line, invocation)
				found = append(found, at)
				return true
			})
		}
	}
	return sortedWarnings(found)
}
