package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// groupImports splits a file's parenthesized import block into standard-library and
// third-party groups separated by a blank line, as goimports would without the x/tools
// dependency, then gofmts the file. A block with only one kind of import is left as is.
func groupImports(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("group imports: parse: %w", err)
	}
	var decl *ast.GenDecl
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT && gd.Lparen.IsValid() {
			decl = gd
			break
		}
	}

	out := src
	if decl != nil && len(decl.Specs) >= 2 {
		var std, third []string
		for _, s := range decl.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok {
				continue
			}
			spec := is.Path.Value
			if is.Name != nil {
				spec = is.Name.Name + " " + spec
			}
			if isThirdPartyImport(is.Path.Value) {
				third = append(third, spec)
			} else {
				std = append(std, spec)
			}
		}
		if len(std) > 0 && len(third) > 0 {
			sort.Strings(std)
			sort.Strings(third)

			var block strings.Builder
			block.WriteString("import (\n")
			for _, s := range std {
				block.WriteString("\t" + s + "\n")
			}
			block.WriteString("\n")
			for _, s := range third {
				block.WriteString("\t" + s + "\n")
			}
			block.WriteString(")")

			start := fset.Position(decl.Pos()).Offset
			end := fset.Position(decl.End()).Offset
			var buf bytes.Buffer
			buf.Write(src[:start])
			buf.WriteString(block.String())
			buf.Write(src[end:])
			out = buf.Bytes()
		}
	}

	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("group imports: gofmt: %w", err)
	}
	return formatted, nil
}

// isThirdPartyImport reports whether a quoted import path is third-party, meaning its first
// path segment contains a "." (e.g. "github.com/...").
func isThirdPartyImport(quotedPath string) bool {
	p := strings.Trim(quotedPath, `"`)
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return strings.Contains(p, ".")
}
