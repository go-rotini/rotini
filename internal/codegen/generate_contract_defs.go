package codegen

import (
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// draft07 is the `$schema` of every self-contained schema rotini writes.
const draft07 = "http://json-schema.org/draft-07/schema#"

// contractDefs holds the contract's named schemas: the root spec's, then each composed spec's.
// A composed spec's schema whose name the document already uses for a different schema is
// renamed `<spec name>.<schema name>`, and that spec's `$ref`s are rewritten to match, so
// every reference in the document resolves to the schema its spec meant.
type contractDefs struct {
	pool    map[string]any                     // definition name → standard JSON Schema
	renamed map[*schemaScope]map[string]string // scope → schema name → definition name, where they differ
	raw     map[string]any                     // definition name → its schema as its own spec wrote it
	scopes  map[*schemaScope]bool              // scopes already added
}

// contractDefinitions gathers the named schemas of every spec the nodes come from.
func (p *program) contractDefinitions(nodes []contractNode) *contractDefs {
	d := &contractDefs{pool: map[string]any{}, renamed: map[*schemaScope]map[string]string{}, raw: map[string]any{}, scopes: map[*schemaScope]bool{}}
	d.add(nil, p.schemas)
	for _, n := range nodes {
		if n.scope != nil && !d.scopes[n.scope] {
			d.add(n.scope, n.scope.schemas)
		}
	}
	return d
}

// add names one spec's schemas, then renders them with their references renamed.
func (d *contractDefs) add(scope *schemaScope, schemas map[string]Schema) {
	d.scopes[scope] = true
	names := slices.Sorted(maps.Keys(schemas))
	defName := map[string]string{}
	for _, name := range names {
		raw := standardSchema(schemaToDoc(schemas[name]))
		taken, exists := d.raw[name]
		switch {
		case !exists:
			defName[name] = name
		case reflect.DeepEqual(taken, raw):
			defName[name] = name
			continue // the same schema, already in the pool
		default:
			alt := scope.name + "." + name
			for i := 2; d.raw[alt] != nil; i++ {
				alt = scope.name + "." + name + "." + strconv.Itoa(i)
			}
			defName[name] = alt
			d.addRename(scope, name, alt)
		}
		d.raw[defName[name]] = raw
	}
	for _, name := range names {
		if def, ok := defName[name]; ok && d.pool[def] == nil {
			d.pool[def] = d.rename(scope, standardSchema(schemaToDoc(schemas[name])))
		}
	}
}

func (d *contractDefs) addRename(scope *schemaScope, from, to string) {
	if d.renamed[scope] == nil {
		d.renamed[scope] = map[string]string{}
	}
	d.renamed[scope][from] = to
}

// rename rewrites, in place, the `$ref`s of a schema from scope's spec to the definition
// names the document uses, and returns it.
func (d *contractDefs) rename(scope *schemaScope, doc any) any {
	names := d.renamed[scope]
	if len(names) == 0 {
		return doc
	}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if ref, ok := t["$ref"].(string); ok {
				if name, ok := strings.CutPrefix(ref, "#/definitions/"); ok && names[name] != "" {
					t["$ref"] = "#/definitions/" + names[name]
				}
			}
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(doc)
	return doc
}

// schemaDoc renders a schema from scope's spec as a self-contained JSON Schema.
func (d *contractDefs) schemaDoc(scope *schemaScope, s Schema) any {
	doc := d.rename(scope, standardSchema(schemaToDoc(s)))
	if m, ok := doc.(map[string]any); ok {
		return d.selfContained(m)
	}
	return doc
}

// selfContained makes doc usable on its own: it declares draft-07 and carries the named
// schemas it reaches. A doc that references a schema the document does not hold is returned
// unchanged.
func (d *contractDefs) selfContained(doc map[string]any) map[string]any {
	defs := map[string]any{}
	queue := schemaRefs(doc)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := defs[name]; done {
			continue
		}
		def, ok := d.pool[name]
		if !ok {
			return doc
		}
		defs[name] = def
		queue = append(queue, schemaRefs(def)...)
	}
	doc["$schema"] = draft07
	if len(defs) > 0 {
		doc["definitions"] = defs
	}
	return doc
}
