package rotinitest

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// CodecDocuments are documents in a codec's format, for [CheckCodec]. Each field holds the
// documents for one rule; leave one nil to skip its check, for a format that can't write such a
// document (one with no null, say).
type CodecDocuments struct {
	// Valid are well-formed documents with a mapping at the top level. Each must decode, twice
	// to the same tree, into maps, lists and scalars only, keeping nothing of its input.
	Valid [][]byte
	// DuplicateKey gives one key twice in a mapping; it must fail to decode.
	DuplicateKey []byte
	// TwoDocuments holds two documents, one after the other; it must fail to decode.
	TwoDocuments []byte
	// Null sets the top-level key "key" to the format's null; it must decode with "key" set to
	// nil.
	Null []byte
}

// CheckCodec checks decode, the decoder of a format wired with [rotini.Codec], against the
// rules the built-in formats follow, reporting each one broken: a key given twice fails, two
// documents fail, null decodes as nil, and a document decodes to the generic tree (a
// map[string]any for each mapping, []any for each list, and strings, bools, numbers, times and
// nil) without keeping its input.
//
//	func TestINI(t *testing.T) {
//		rotinitest.CheckCodec(t, decodeINI, rotinitest.CodecDocuments{
//			Valid:        [][]byte{[]byte("[deploy]\nreplicas = 3\n")},
//			DuplicateKey: []byte("a = 1\na = 2\n"),
//		})
//	}
func CheckCodec(tb testing.TB, decode func(data []byte) (any, error), docs CodecDocuments) {
	tb.Helper()
	if decode == nil {
		tb.Fatalf("rotinitest.CheckCodec: nil decode")
	}
	for i, doc := range docs.Valid {
		name := "Valid[" + strconv.Itoa(i) + "]"
		first, err := decode(slices.Clone(doc))
		if err != nil {
			tb.Errorf("%s: decode failed: %v", name, err)
			continue
		}
		if _, ok := first.(map[string]any); !ok {
			tb.Errorf("%s: decoded to a %T; a document's top level must be a map[string]any", name, first)
			continue
		}
		if problem := genericTree(first, ""); problem != "" {
			tb.Errorf("%s: %s", name, problem)
		}
		scratch := slices.Clone(doc)
		kept, err := decode(scratch)
		if err != nil {
			tb.Errorf("%s: decode failed the second time: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(kept, first) {
			tb.Errorf("%s: decoded differently the second time:\n%#v\nthen\n%#v", name, first, kept)
			continue
		}
		for j := range scratch {
			scratch[j] = 'x'
		}
		if !reflect.DeepEqual(kept, first) {
			tb.Errorf("%s: the decoded tree changed when its input was overwritten; decode must copy what it keeps", name)
		}
	}
	if docs.DuplicateKey != nil {
		if v, err := decode(slices.Clone(docs.DuplicateKey)); err == nil {
			tb.Errorf("DuplicateKey: decoded to %#v; a key given twice must be an error", v)
		}
	}
	if docs.TwoDocuments != nil {
		if v, err := decode(slices.Clone(docs.TwoDocuments)); err == nil {
			tb.Errorf("TwoDocuments: decoded to %#v; more than one document must be an error", v)
		}
	}
	if docs.Null != nil {
		v, err := decode(slices.Clone(docs.Null))
		m, _ := v.(map[string]any)
		val, set := m["key"]
		switch {
		case err != nil:
			tb.Errorf("Null: decode failed: %v", err)
		case !set:
			tb.Errorf("Null: decoded to %#v, with no \"key\"", v)
		case val != nil:
			tb.Errorf("Null: \"key\" decoded to %#v (%T); null must decode as nil", val, val)
		}
	}
}

// CheckCodecEncode checks encode, the encoder a [rotini.CodecEncode] gives a format, against
// its decoder: for each of docs.Valid, encoding the decoded tree must succeed, leave the tree
// as it was, and decode back to the same tree. The other fields of docs are not used.
//
//	func TestINIEncode(t *testing.T) {
//		rotinitest.CheckCodecEncode(t, decodeINI, encodeINI, rotinitest.CodecDocuments{
//			Valid: [][]byte{[]byte("name = web\n[deploy]\nreplicas = 3\n")},
//		})
//	}
func CheckCodecEncode(tb testing.TB, decode func(data []byte) (any, error), encode func(doc any) ([]byte, error), docs CodecDocuments) {
	tb.Helper()
	if decode == nil || encode == nil {
		tb.Fatalf("rotinitest.CheckCodecEncode: nil decode or encode")
	}
	for i, doc := range docs.Valid {
		name := "Valid[" + strconv.Itoa(i) + "]"
		tree, err := decode(slices.Clone(doc))
		if err != nil {
			tb.Errorf("%s: decode failed: %v", name, err)
			continue
		}
		want, err := decode(slices.Clone(doc))
		if err != nil {
			tb.Errorf("%s: decode failed the second time: %v", name, err)
			continue
		}
		out, err := encode(tree)
		if err != nil {
			tb.Errorf("%s: encode failed: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(tree, want) {
			tb.Errorf("%s: encode changed the tree it was given:\n%#v\nwant\n%#v", name, tree, want)
			continue
		}
		back, err := decode(slices.Clone(out))
		if err != nil {
			tb.Errorf("%s: what encode wrote doesn't decode: %v\n%s", name, err, out)
			continue
		}
		if !reflect.DeepEqual(back, want) {
			tb.Errorf("%s: what encode wrote decodes to a different tree:\n%#v\nwant\n%#v\nencoded:\n%s", name, back, want, out)
		}
	}
}

// genericTree describes the first value in v that isn't part of the generic tree, with its
// path, or returns "".
func genericTree(v any, path string) string {
	switch t := v.(type) {
	case nil, bool, string, json.Number, time.Time,
		int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return ""
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			if problem := genericTree(t[k], sub); problem != "" {
				return problem
			}
		}
		return ""
	case []any:
		for i, e := range t {
			if problem := genericTree(e, path+"["+strconv.Itoa(i)+"]"); problem != "" {
				return problem
			}
		}
		return ""
	}
	at := path
	if at == "" {
		at = "the top level"
	}
	return fmt.Sprintf("%s is a %T; decode must return map[string]any, []any, strings, bools, numbers, times and nil", at, v)
}
