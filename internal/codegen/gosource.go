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

// Go-source AST surgery shared by renderGoFile (import grouping) and mergeRuntime
// (split + re-merge): parse a gofmt'd file into imports + body, and regroup a merged
// import block into the std / third-party convention.

// splitGoFile parses a gofmt'd Go source file and returns its import specs (each
// reconstructed as it appears in source, e.g. `_ "embed"` or `"fmt"`) and the
// file body verbatim — everything after the import block (or after the package
// clause when there are no imports). Returning the body as raw source preserves
// directive comments like //go:embed exactly.
func splitGoFile(src []byte) (imports []string, body []byte, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated source: %w", err)
	}
	bodyStart := fset.Position(f.Name.End()).Offset
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, s := range gd.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok {
				continue // an IMPORT decl holds only ImportSpecs
			}
			if is.Name != nil {
				imports = append(imports, is.Name.Name+" "+is.Path.Value)
			} else {
				imports = append(imports, is.Path.Value)
			}
		}
		if e := fset.Position(gd.End()).Offset; e > bodyStart {
			bodyStart = e
		}
	}
	body = bytes.TrimLeft(src[bodyStart:], "\n\r\t ")
	return imports, body, nil
}

// groupImports rewrites a Go source file's single gofmt'd import block into the two
// conventional groups — standard library first, then third-party — separated by a
// blank line, and re-formats. gofmt sorts imports but never splits std from
// third-party (that is goimports' job); the templates emit one merged block, so this
// restores the idiom without taking on the golang.org/x/tools dependency. A file
// with fewer than two imports, or whose imports already fall in a single group, is
// returned gofmt'd but otherwise unchanged.
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

	// Rearrange only when there is a parenthesized block holding both a standard-
	// library and a third-party group; otherwise the block is already conventional
	// and gofmt-formatted as-is below.
	out := src
	if decl != nil && len(decl.Specs) >= 2 {
		var std, third []string
		for _, s := range decl.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok {
				continue // an IMPORT decl holds only ImportSpecs
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

// isThirdPartyImport reports whether a quoted import path is a third-party package —
// its first path segment contains a "." (e.g. "github.com/..."). Standard-library
// paths ("fmt", "text/template", "embed") have no dot in the first segment.
func isThirdPartyImport(quotedPath string) bool {
	p := strings.Trim(quotedPath, `"`)
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return strings.Contains(p, ".")
}
