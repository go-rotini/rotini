package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// fmtSchema is what `rotini fmt` needs from a JSON Schema object: the definition it is, its
// property names in the order the schema declares them, and the schema of each property, of
// list items, and of the values of a map with free-form keys.
type fmtSchema struct {
	kind       string // the definition, such as "Command"; "" for an inline object
	props      []string
	prop       map[string]*fmtSchema
	items      *fmtSchema
	additional *fmtSchema
}

// property returns the schema of key: a declared property, or a map's value schema.
func (s *fmtSchema) property(key string) *fmtSchema {
	if s == nil {
		return nil
	}
	if p, ok := s.prop[key]; ok {
		return p
	}
	return s.additional
}

func (s *fmtSchema) itemSchema() *fmtSchema {
	if s == nil {
		return nil
	}
	return s.items
}

// jsonNode is a decoded JSON value that keeps object key order.
type jsonNode struct {
	keys []string
	obj  map[string]*jsonNode
	arr  []*jsonNode
	str  string
}

func decodeOrdered(data []byte) (*jsonNode, error) {
	n, err := readOrdered(json.NewDecoder(bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	return n, nil
}

//nolint:wrapcheck // decodeOrdered wraps the error once, at the top.
func readOrdered(dec *json.Decoder) (*jsonNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	n := &jsonNode{}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n.obj = map[string]*jsonNode{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				v, err := readOrdered(dec)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, key)
				n.obj[key] = v
			}
		case '[':
			for dec.More() {
				v, err := readOrdered(dec)
				if err != nil {
					return nil, err
				}
				n.arr = append(n.arr, v)
			}
		}
		if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	case string:
		n.str = t
	}
	return n, nil
}

// fmtSchemas resolves the embedded spec and conf schemas once.
type fmtSchemas struct {
	spec, conf, command *fmtSchema
}

var loadFmtSchemas = sync.OnceValues(func() (*fmtSchemas, error) {
	spec, err := buildFmtSchema(schemaSpecFileBytes, "")
	if err != nil {
		return nil, fmt.Errorf("read the spec schema: %w", err)
	}
	command, err := buildFmtSchema(schemaSpecFileBytes, "#/definitions/Command")
	if err != nil {
		return nil, fmt.Errorf("read the spec schema: %w", err)
	}
	conf, err := buildFmtSchema(schemaConfFileBytes, "")
	if err != nil {
		return nil, fmt.Errorf("read the conf schema: %w", err)
	}
	return &fmtSchemas{spec: spec, conf: conf, command: command}, nil
})

// buildFmtSchema resolves the schema at ref ("" for the root) of the schema document data.
func buildFmtSchema(data []byte, ref string) (*fmtSchema, error) {
	root, err := decodeOrdered(data)
	if err != nil {
		return nil, err
	}
	b := &fmtSchemaBuilder{root: root, done: map[*jsonNode]*fmtSchema{}, defs: map[*jsonNode]string{}}
	if defs := root.obj["definitions"]; defs != nil {
		for name, def := range defs.obj {
			b.defs[def] = name
		}
	}
	start := root
	if ref != "" {
		if start = b.resolve(ref); start == nil {
			return nil, fmt.Errorf("no %s", ref)
		}
	}
	return b.build(start), nil
}

type fmtSchemaBuilder struct {
	root *jsonNode
	done map[*jsonNode]*fmtSchema
	defs map[*jsonNode]string // definition node to its name
}

// resolve follows a local "#/a/b" reference.
func (b *fmtSchemaBuilder) resolve(ref string) *jsonNode {
	n := b.root
	for seg := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
		if n == nil || n.obj == nil {
			return nil
		}
		n = n.obj[seg]
	}
	return n
}

// build turns schema object n into a fmtSchema, merging what $ref, allOf, anyOf and oneOf
// contribute, in order. Recursive schemas (a Command's commands) share one fmtSchema.
func (b *fmtSchemaBuilder) build(n *jsonNode) *fmtSchema {
	if s, ok := b.done[n]; ok {
		return s
	}
	s := &fmtSchema{prop: map[string]*fmtSchema{}}
	b.done[n] = s
	b.merge(s, n, map[*jsonNode]bool{})
	return s
}

func (b *fmtSchemaBuilder) merge(s *fmtSchema, n *jsonNode, seen map[*jsonNode]bool) {
	if n == nil || n.obj == nil || seen[n] {
		return
	}
	seen[n] = true
	if name, ok := b.defs[n]; ok && s.kind == "" {
		s.kind = name
	}
	if ref := n.obj["$ref"]; ref != nil && ref.str != "" {
		b.merge(s, b.resolve(ref.str), seen)
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if list := n.obj[key]; list != nil {
			for _, sub := range list.arr {
				b.merge(s, sub, seen)
			}
		}
	}
	if props := n.obj["properties"]; props != nil {
		for _, name := range props.keys {
			if _, dup := s.prop[name]; !dup {
				s.props = append(s.props, name)
			}
			s.prop[name] = b.build(props.obj[name])
		}
	}
	if items := n.obj["items"]; items != nil && items.obj != nil && s.items == nil {
		s.items = b.build(items)
	}
	if add := n.obj["additionalProperties"]; add != nil && add.obj != nil && s.additional == nil {
		s.additional = b.build(add)
	}
}
