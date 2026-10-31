package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// This file keeps the generated package-level literals static data. A literal with a call or a
// map in it gets an init function that fills it, and an init function is always linked, so a
// composed child's whole Definition would stay in a parent's binary though the parent reads only
// its own. Pointer fields point at package variables instead of new(...), and the inputs and
// output types and maps are set on first use (see definitionFill).

// staticDecl is a rendered `var <name> = <literal>` declaration made static.
type staticDecl struct {
	decl  string   // the declaration, with the dynamic parts taken out
	fills []string // assignments, relative to the variable, that put the dynamic parts back
}

// pointerVars collects the package variables a static literal's pointer fields point at,
// named for their value so equal values share one.
type pointerVars map[string]string // name -> Go value

// decl renders the variables' declaration, or "" when there are none.
func (pv pointerVars) decl() string {
	if len(pv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n// The values the definition's and InputSettings' pointer fields point at.\nvar (\n")
	for _, name := range slices.Sorted(maps.Keys(pv)) {
		fmt.Fprintf(&b, "%s = %s\n", name, pv[name])
	}
	b.WriteString(")\n")
	return b.String()
}

// pointerTo returns the package variable a new(arg) can point at instead, recording it, or
// "" when arg is not a bool, int or float64 constant.
func (pv pointerVars) pointerTo(arg string) string {
	var name, value string
	switch {
	case arg == "true" || arg == "false":
		name, value = "rotini"+strings.ToUpper(arg[:1])+arg[1:], arg
	case intConstant().MatchString(arg):
		name, value = "rotiniInt"+constantSuffix(arg), arg
	default:
		m := floatConstant().FindStringSubmatch(arg)
		if m == nil {
			return ""
		}
		name, value = "rotiniFloat"+constantSuffix(m[1]), arg
	}
	pv[name] = value
	return name
}

// constantSuffix spells a numeric constant as identifier characters: "-" as N, "." as p, and
// an exponent's "+" dropped (strconv always writes an exponent's sign, so this stays unique).
func constantSuffix(s string) string {
	return strings.NewReplacer("-", "N", ".", "p", "+", "").Replace(s)
}

// intConstant matches an int constant as a generated literal spells it.
func intConstant() *regexp.Regexp {
	return intConstantOnce.value(func() *regexp.Regexp { return regexp.MustCompile(`^-?\d+$`) })
}

var intConstantOnce lazy[*regexp.Regexp]

// floatConstant matches a float64(<constant>) conversion as a generated literal spells it.
func floatConstant() *regexp.Regexp {
	return floatConstantOnce.value(func() *regexp.Regexp {
		return regexp.MustCompile(`^float64\((-?\d+(?:\.\d+)?(?:e[-+]\d+)?)\)$`)
	})
}

var floatConstantOnce lazy[*regexp.Regexp]

// makeStatic rewrites src, one `var <name> = <composite literal>` declaration, so the literal
// is static: each new(<constant>) points at a variable in pv, and, when move is set, each
// rotiniTypeOf call, other new call and map literal is taken out and returned as an
// assignment (`.Commands[0].Inputs = ...`) to make on first use. Without move those stay put.
// The rest of src is kept byte for byte.
func makeStatic(src string, pv pointerVars, move bool) (staticDecl, error) {
	doc, body := splitDocComment(src)
	const pkg = "package p\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", pkg+body, parser.ParseComments)
	if err != nil {
		return staticDecl{}, fmt.Errorf("parse the generated literal: %w", err)
	}
	lit := soleVarLiteral(file)
	if lit == nil || len(file.Comments) > 0 {
		return staticDecl{decl: src}, nil
	}
	s := staticizer{src: body, base: fset.File(file.Pos()).Base() + len(pkg), pv: pv, move: move}
	s.literal(lit, "")
	out := body
	for _, e := range slices.Backward(s.edits) {
		out = out[:e.from] + e.text + out[e.to:]
	}
	return staticDecl{decl: doc + out, fills: s.fills}, nil
}

// soleVarLiteral returns the composite literal file's one declaration, `var x = <literal>`,
// assigns, or nil when file is not that.
func soleVarLiteral(file *ast.File) *ast.CompositeLit {
	if len(file.Decls) != 1 {
		return nil
	}
	gd, ok := file.Decls[0].(*ast.GenDecl)
	if !ok || len(gd.Specs) != 1 {
		return nil
	}
	vs, ok := gd.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Values) != 1 {
		return nil
	}
	lit, _ := vs.Values[0].(*ast.CompositeLit)
	return lit
}

// splitDocComment splits src into its leading // comment lines and the rest.
func splitDocComment(src string) (doc, body string) {
	body = src
	for strings.HasPrefix(body, "//") {
		end := strings.IndexByte(body, '\n')
		if end < 0 {
			return src, ""
		}
		body = body[end+1:]
	}
	return src[:len(src)-len(body)], body
}

// staticizer walks a composite literal for makeStatic, collecting text edits in source order.
type staticizer struct {
	src   string // the declaration
	base  int    // the file position of src's first byte
	pv    pointerVars
	move  bool
	fills []string
	edits []textEdit
}

// textEdit replaces src[from:to] with text.
type textEdit struct {
	from, to int
	text     string
}

// literal makes the elements of lit, found at path below the variable, static.
func (s *staticizer) literal(lit *ast.CompositeLit, path string) {
	for i, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			key, isField := kv.Key.(*ast.Ident)
			if !isField {
				continue // a keyed array element; generated literals have none
			}
			if s.value(kv.Value, path+"."+key.Name) {
				s.drop(kv)
			}
			continue
		}
		if s.value(e, path+"["+strconv.Itoa(i)+"]") {
			s.edits = append(s.edits, textEdit{s.off(e.Pos()), s.off(e.End()), "nil"})
		}
	}
}

// value makes e, found at path, static, reporting whether it moved into a fill and so must
// leave the literal.
func (s *staticizer) value(e ast.Expr, path string) bool {
	switch x := e.(type) {
	case *ast.CallExpr:
		fn, ok := x.Fun.(*ast.Ident)
		if !ok || (fn.Name != "new" && fn.Name != "rotiniTypeOf") {
			return false
		}
		if fn.Name == "new" && len(x.Args) == 1 {
			if name := s.pv.pointerTo(s.text(x.Args[0])); name != "" {
				s.edits = append(s.edits, textEdit{s.off(x.Pos()), s.off(x.End()), "&" + name})
				return false
			}
		}
		return s.moveOut(e, path)
	case *ast.CompositeLit:
		if _, isMap := x.Type.(*ast.MapType); isMap {
			return s.moveOut(e, path)
		}
		s.literal(x, path)
	case *ast.UnaryExpr:
		if lit, ok := x.X.(*ast.CompositeLit); ok && x.Op == token.AND {
			s.literal(lit, path)
		}
	}
	return false
}

// moveOut records e as a fill at path when moving, reporting whether it did.
func (s *staticizer) moveOut(e ast.Expr, path string) bool {
	if !s.move {
		return false
	}
	s.fills = append(s.fills, path+" = "+s.text(e))
	return true
}

// drop removes the keyed element kv with its comma, and its whole line when it has one to
// itself.
func (s *staticizer) drop(kv *ast.KeyValueExpr) {
	from, to := s.off(kv.Pos()), s.off(kv.End())
	to += len(s.src[to:]) - len(strings.TrimLeft(s.src[to:], " \t"))
	if strings.HasPrefix(s.src[to:], ",") {
		to++
	}
	lineStart := strings.LastIndexByte(s.src[:from], '\n') + 1
	rest := strings.TrimLeft(s.src[to:], " \t")
	if strings.TrimLeft(s.src[lineStart:from], " \t") == "" && strings.HasPrefix(rest, "\n") {
		from, to = lineStart, len(s.src)-len(rest)+1
	}
	s.edits = append(s.edits, textEdit{from, to, ""})
}

// off is p's byte offset in src.
func (s *staticizer) off(p token.Pos) int { return int(p) - s.base }

// text is e's source.
func (s *staticizer) text(e ast.Expr) string { return s.src[s.off(e.Pos()):s.off(e.End())] }

// definitionFill renders the function that sets the definition's dynamic parts on first use,
// or "" when it has none and definition is read directly.
func definitionFill(fills []string) string {
	if len(fills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nvar rotiniDefinitionOnce sync.Once\n\n")
	b.WriteString("// rotiniDefinition returns definition with its inputs and output types and its maps set,\n")
	b.WriteString("// which the first call does. Until then definition holds only static data, so a program\n")
	b.WriteString("// that composes this package and never builds it doesn't keep it.\n")
	fmt.Fprintf(&b, "func rotiniDefinition() %s.Definition {\n", rotiniPkgName)
	b.WriteString("rotiniDefinitionOnce.Do(func() {\n")
	for _, f := range fills {
		b.WriteString("definition")
		b.WriteString(f)
		b.WriteByte('\n')
	}
	b.WriteString("})\nreturn definition\n}\n")
	return b.String()
}
