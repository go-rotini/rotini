package rotini

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
)

// customLayer is the provenance a hand-built layer's field gets when neither its Presence nor
// the layer names a source.
const customLayer = "custom"

// handBuiltSource is one field a hand-built layer supplied: the field's path, its index in the
// report's history, and the layer's values.
type handBuiltSource struct {
	path   FieldPath
	at     int
	values reflect.Value
}

// handBuiltProvenance names a hand-built layer's field by the layer's Name when its Presence
// left the source unnamed (empty, or the "custom" that [PresenceOf] writes).
func handBuiltProvenance(prov InputSource, name string) InputSource {
	if name != "" && (prov.Layer == "" || prov.Layer == customLayer) {
		prov.Layer = name
	} else if prov.Layer == "" {
		prov.Layer = customLayer
	}
	return prov
}

// fillHandBuiltRaw completes the provenance of the fields hand-built layers supplied: an empty
// Raw gets the supplied value's text, and a secret field's Raw is always redacted. It needs a
// rotini layer in the merge to know which fields are secret, so without one it leaves Raw as
// the layer gave it.
func (r *InputReport) fillHandBuiltRaw(hand []handBuiltSource) {
	if len(hand) == 0 || len(r.chain) == 0 {
		return
	}
	secret := r.secretFields()
	for _, h := range hand {
		prov := &r.history[h.path][h.at]
		switch {
		case secret[h.path]:
			prov.Raw = redactedValue
		case prov.Raw == "":
			prov.Raw = handBuiltText(h.values, h.path)
		}
		if h.at == len(r.history[h.path])-1 {
			r.set[h.path] = *prov
		}
	}
}

// handBuiltText is the text of the field at path in v, as a channel layer records it: a list's
// values joined with ", ". It is "" for a field with no text form: a map, an object, a stdin
// document or stream, or a nil pointer.
func handBuiltText(v reflect.Value, path FieldPath) string {
	if strings.HasSuffix(string(path), ".Stdin") {
		return ""
	}
	for seg := range strings.SplitSeq(string(path), ".") {
		if v.Kind() != reflect.Struct {
			return ""
		}
		if v = v.FieldByName(seg); !v.IsValid() {
			return ""
		}
	}
	elems, _, ok := typedElems(v)
	if !ok || elems == nil {
		return ""
	}
	texts := make([]string, 0, len(elems))
	for _, e := range elems {
		if !textual(e) {
			return ""
		}
		texts = append(texts, typedText(e))
	}
	return strings.Join(texts, ", ")
}

// textual reports whether typedText renders e as a user would have typed it, rather than as Go
// syntax for an object or a function.
func textual(e reflect.Value) bool {
	switch e.Kind() {
	case reflect.Struct, reflect.Map, reflect.Func, reflect.Chan, reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Array:
		if !e.CanInterface() {
			return false
		}
		_, text := reflect.TypeAssert[encoding.TextMarshaler](e)
		_, str := reflect.TypeAssert[fmt.Stringer](e)
		return text || str
	}
	return true
}
