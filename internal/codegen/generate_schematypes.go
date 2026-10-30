package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// jsonSchemaTypes rewrites, in place, each `type` written as a rotini type name (int, bool,
// []string, duration) into its JSON Schema spelling, which the spec treats as equivalent, and
// returns v. Any other type, such as a named or imported Go type, is left as written.
func jsonSchemaTypes(v any) any {
	t, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, val := range t {
		switch k {
		case "properties", "definitions":
			if m, ok := val.(map[string]any); ok {
				for name, sub := range m {
					m[name] = jsonSchemaTypes(sub)
				}
			}
		case "items", "additionalProperties":
			t[k] = jsonSchemaTypes(val)
		case "allOf", "anyOf", "oneOf":
			if list, ok := val.([]any); ok {
				for i := range list {
					list[i] = jsonSchemaTypes(list[i])
				}
			}
		}
	}
	if typ, ok := t["type"].(string); ok && rotiniTypeName(typ) {
		delete(t, "type")
		maps.Copy(t, standardType(typ, t))
	}
	return t
}

// rotiniTypeName reports whether typ is a type name standardType maps that isn't already JSON
// Schema's, element types included.
func rotiniTypeName(typ string) bool {
	switch typ {
	case "string", "integer", "number", "boolean", "object", "array", "null":
		return false
	}
	var known func(string) bool
	known = func(t string) bool {
		if elem, ok := strings.CutPrefix(t, "[]"); ok {
			return known(elem)
		}
		if _, val, ok := splitMapType(t); ok {
			return known(val)
		}
		return len(standardType(t, map[string]any{})) > 0
	}
	return known(typ)
}

// goTypeHint is the key typeDoc records a sized number type under, since its JSON Schema
// spelling (integer, number) loses the size. Only the generated Go types read it.
const goTypeHint = "x-rotini-go-type"

// sizedNumbers are the number types JSON Schema writes as integer or number but Go keeps apart.
var sizedNumbers = map[string]bool{
	"int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true,
}

// typeDoc is a schema as the JSON Schema the generated Go types are built from: [schemaToDoc]
// then [jsonSchemaTypes], with each sized number type recorded under goTypeHint for
// [sizedNumberTypes].
func typeDoc(s Schema) any {
	doc := schemaToDoc(s)
	hintSizedNumbers(doc)
	return jsonSchemaTypes(doc)
}

// hintSizedNumbers records under goTypeHint each `type` whose numbers are sized ("uint64",
// "[]int32", "map[string]float32"), walking the same keywords as [jsonSchemaTypes].
func hintSizedNumbers(v any) {
	t, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range t {
		switch k {
		case "properties", "definitions":
			if m, ok := val.(map[string]any); ok {
				for _, sub := range m {
					hintSizedNumbers(sub)
				}
			}
		case "items", "additionalProperties":
			hintSizedNumbers(val)
		case "allOf", "anyOf", "oneOf":
			if list, ok := val.([]any); ok {
				for _, sub := range list {
					hintSizedNumbers(sub)
				}
			}
		}
	}
	if typ, ok := t["type"].(string); ok && sizedNumberType(typ) {
		t[goTypeHint] = jsonSchemaTypeToGo(typ)
	}
}

// sizedNumberType reports whether typ is a sized number, or a list or map whose elements are.
func sizedNumberType(typ string) bool {
	if elem, ok := strings.CutPrefix(typ, "[]"); ok {
		return sizedNumberType(elem)
	}
	if _, val, ok := splitMapType(typ); ok {
		return sizedNumberType(val)
	}
	return sizedNumbers[typ]
}

// sizedNumberTypes rewrites the generated types so each field or type whose schema carries
// goTypeHint has that Go type (uint64, not the int an integer generates), keeping a nullable
// field's pointer. It follows each definition's fields by JSON tag, into the types generated
// for inline objects, list elements and map values.
func sizedNumberTypes(src string, defs map[string]any) (string, error) {
	const pkgClause = "package p\n\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", pkgClause+src, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse generated types: %w", err)
	}
	specs := map[string]*ast.TypeSpec{}
	ast.Inspect(file, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			specs[ts.Name.Name] = ts
		}
		return true
	})
	type edit struct {
		off, end int
		text     string
	}
	var edits []edit
	visited := map[string]bool{}
	var visitType func(name string, sch map[string]any)
	var visitExpr func(expr ast.Expr, sch map[string]any)
	visitExpr = func(expr ast.Expr, sch map[string]any) {
		if sch == nil {
			return
		}
		if hint, ok := sch[goTypeHint].(string); ok {
			if _, star := expr.(*ast.StarExpr); star {
				hint = "*" + hint
			}
			edits = append(edits, edit{fset.Position(expr.Pos()).Offset, fset.Position(expr.End()).Offset, hint})
			return
		}
		switch e := expr.(type) {
		case *ast.StarExpr:
			visitExpr(e.X, sch)
		case *ast.ArrayType:
			items, _ := sch["items"].(map[string]any)
			visitExpr(e.Elt, items)
		case *ast.MapType:
			values, _ := sch["additionalProperties"].(map[string]any)
			visitExpr(e.Value, values)
		case *ast.Ident:
			if _, ref := sch["$ref"]; !ref {
				visitType(e.Name, sch) // an inline object, generated as a type of its own
			}
		}
	}
	visitType = func(name string, sch map[string]any) {
		ts, ok := specs[name]
		if !ok || visited[name] {
			return
		}
		visited[name] = true
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			visitExpr(ts.Type, sch)
			return
		}
		props := schemaProperties(sch)
		for _, f := range st.Fields.List {
			if f.Tag == nil || len(f.Names) == 0 {
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}
			key, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			visitExpr(f.Type, props[key])
		}
	}
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		sch, _ := defs[name].(map[string]any)
		visitType(name, sch)
	}
	out := pkgClause + src
	sort.Slice(edits, func(i, j int) bool { return edits[i].off > edits[j].off })
	for _, e := range edits {
		out = out[:e.off] + e.text + out[e.end:]
	}
	return strings.TrimPrefix(out, pkgClause), nil
}

// schemaProperties is an object schema's properties by name, those of its inline allOf members
// included, as the generated struct merges them.
func schemaProperties(sch map[string]any) map[string]map[string]any {
	props := map[string]map[string]any{}
	add := func(m map[string]any) {
		p, _ := m["properties"].(map[string]any)
		for k, v := range p {
			if vm, ok := v.(map[string]any); ok {
				props[k] = vm
			}
		}
	}
	if list, ok := sch["allOf"].([]any); ok {
		for _, sub := range list {
			if m, ok := sub.(map[string]any); ok {
				add(m)
			}
		}
	}
	add(sch)
	return props
}
