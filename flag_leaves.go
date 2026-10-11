package rotini

import (
	"iter"
	"reflect"
	"sync"
)

// structLeaves yields each field of the struct s with its value, reaching through anonymous
// embedded structs to their promoted fields: a command's Flags struct embeds one struct per
// flag set it uses, whose fields bind like its own. The embedded field itself is not yielded,
// and a field's name is its promoted name, so a FieldPath reads Top.Flags.Format either way.
// It is small enough to inline, so ranging over it allocates nothing of its own.
func structLeaves(s reflect.Value) iter.Seq2[reflect.StructField, reflect.Value] {
	return func(yield func(reflect.StructField, reflect.Value) bool) { walkLeaves(s, yield) }
}

// typeLeaves is structLeaves for a type, without values.
func typeLeaves(t reflect.Type) iter.Seq[reflect.StructField] {
	return func(yield func(reflect.StructField) bool) {
		walkLeaves(reflect.Zero(t), func(sf reflect.StructField, _ reflect.Value) bool { return yield(sf) })
	}
}

// walkLeaves is the loop behind structLeaves.
func walkLeaves(s reflect.Value, yield func(reflect.StructField, reflect.Value) bool) {
	t := s.Type()
	if !embedsStruct(t) {
		for j := range t.NumField() {
			if !yield(t.Field(j), s.Field(j)) {
				return
			}
		}
		return
	}
	for _, sf := range reflect.VisibleFields(t) {
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			continue
		}
		if !yield(sf, s.FieldByIndex(sf.Index)) {
			return
		}
	}
}

// embedding caches embedsStruct by type, since every input walk asks.
var embedding sync.Map // reflect.Type -> bool

// embedsStruct reports whether t has an anonymous struct field.
func embedsStruct(t reflect.Type) bool {
	if v, ok := embedding.Load(t); ok {
		if found, ok := v.(bool); ok {
			return found
		}
	}
	found := false
	for f := range t.Fields() {
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			found = true
			break
		}
	}
	embedding.Store(t, found)
	return found
}
