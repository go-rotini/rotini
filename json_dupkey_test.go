package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-rotini/jsonc"
)

// jsoncDuplicateKey is the repeated key jsonc.Unmarshal reports for data, or nil.
func jsoncDuplicateKey(data []byte) *jsonc.DuplicateKeyError {
	var probe any
	e, _ := errors.AsType[*jsonc.DuplicateKeyError](jsonc.Unmarshal(data, &probe))
	return e
}

// tokenDuplicateKey is jsonDuplicateKey written over encoding/json's tokens, as a reference.
func tokenDuplicateKey(data []byte) *jsonc.DuplicateKeyError {
	type frame struct {
		obj, wantKey bool
		keys         map[string]bool
		pending      *jsonc.DuplicateKeyError
	}
	var stack []*frame
	done := func() { // a value ended in the frame on top
		if n := len(stack); n > 0 && stack[n-1].obj {
			stack[n-1].wantKey = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			panic(err) // the document is valid JSON
		}
		var top *frame
		if n := len(stack); n > 0 {
			top = stack[n-1]
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				stack = append(stack, &frame{obj: t == '{', wantKey: t == '{', keys: map[string]bool{}})
				continue
			}
			if top.pending != nil {
				return top.pending
			}
			stack = stack[:len(stack)-1]
			done()
		case string:
			if top != nil && top.wantKey {
				if top.pending != nil {
					return top.pending
				}
				top.wantKey = false
				if top.keys[t] {
					start := int(before) + bytes.IndexByte(data[before:], '"')
					line, col := jsonPosition(data[:start])
					top.pending = &jsonc.DuplicateKeyError{Key: t, Pos: jsonc.Position{Line: line, Column: col, Offset: start}}
				}
				top.keys[t] = true
				continue
			}
			done()
		default:
			done()
		}
	}
}

// shallowUTF8 reports whether data nests fewer than 50 deep and is valid UTF-8, where every
// jsonc version reports repeated keys the same way.
func shallowUTF8(data []byte) bool {
	depth, deepest := 0, 0
	for _, c := range data {
		switch c {
		case '{', '[':
			depth++
			deepest = max(deepest, depth)
		case '}', ']':
			depth--
		}
	}
	return deepest < 50 && utf8.Valid(data)
}

func TestJSONDuplicateKey(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", 200) + `{"a":1,"a":2}` + strings.Repeat("]", 200)
	deepValue := `{"a":1,"a":` + strings.Repeat("[", 200) + strings.Repeat("]", 200) + `}`
	var many strings.Builder
	many.WriteString("{")
	for i := range 40 {
		fmt.Fprintf(&many, `"k%d":%d,`, i, i)
	}
	many.WriteString(`"k7":0}`)
	for doc, want := range map[string]string{
		`{"a":1,"b":2}`:     "",
		`[{"a":1},{"a":2}]`: "",
		`{"a":1,"a":2}`:     "a 1:8",
		`[{"a":1,"a":2}]`:   "a 1:9",
		"{\"a\":1,\r\n \"b\":{\"c\":1},\r \"a\":3}": "a 3:2",
		`{"a":1,"\u0061":2}`:                        "a 1:8",
		`{"\/":1,"/":2}`:                            "/ 1:9",
		`{"a":1,"a":{"b":1,"b":2}}`:                 "b 1:19",
		`{"x":{"y":1,"y":2},"x":3}`:                 "y 1:13",
		`{"s":"a\"b\\","s":"{"}`:                    "s 1:15",
		"{\"\xff\":1,\"\xfe\":2}":                   "\ufffd 1:8",
		`{"a":[1,true,null,-2.5e3,"x"],"a":{}}`:     "a 1:31",
		"  \n {\"é\":1, \"é\" : 2}":                 "é 2:11",
		deep:                                        "a 1:208",
		deepValue:                                   "a 1:8",
		many.String():                               "k7 1:342",
	} {
		got, s := jsonDuplicateKey([]byte(doc)), ""
		if got != nil {
			s = fmt.Sprintf("%s %d:%d", got.Key, got.Pos.Line, got.Pos.Column)
		}
		if s != want {
			t.Errorf("%.40q: got %q, want %q", doc, s, want)
		}
		if ref := tokenDuplicateKey([]byte(doc)); !sameDuplicate(got, ref) {
			t.Errorf("%.40q: got %+v, the token walk %+v", doc, got, ref)
		}
		if shallowUTF8([]byte(doc)) && !sameDuplicate(got, jsoncDuplicateKey([]byte(doc))) {
			t.Errorf("%.40q: got %+v, jsonc %+v", doc, got, jsoncDuplicateKey([]byte(doc)))
		}
	}
}

func sameDuplicate(a, b *jsonc.DuplicateKeyError) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Key == b.Key && a.Pos == b.Pos
}

// FuzzJSONDuplicateKey checks that the duplicate-key scan of a valid JSON document finds what a
// walk over encoding/json's tokens finds, and what jsonc reports for a shallow UTF-8 document.
func FuzzJSONDuplicateKey(f *testing.F) {
	for _, seed := range []string{`{"a":1,"a":2}`, `[{"a":{"b":1,"b":2},"a":0}]`, `{"\u0061":1,"a":2}`, "{\"a\":1,\r\"a\":2}", "{\"\xff\":1,\"\xfe\":2}"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		got := jsonDuplicateKey(data)
		if ref := tokenDuplicateKey(data); !sameDuplicate(got, ref) {
			t.Fatalf("%q: got %+v, the token walk %+v", data, got, ref)
		}
		if shallowUTF8(data) && !sameDuplicate(got, jsoncDuplicateKey(data)) {
			t.Fatalf("%q: got %+v, jsonc %+v", data, got, jsoncDuplicateKey(data))
		}
	})
}

func BenchmarkJSONDuplicateKey(b *testing.B) {
	var doc bytes.Buffer
	doc.WriteByte('[')
	for i := range 50000 {
		if i > 0 {
			doc.WriteByte(',')
		}
		fmt.Fprintf(&doc, `{"id":%d,"name":"item-%d","tags":["a","b","c"],"ok":true}`, i, i)
	}
	doc.WriteByte(']')
	data := doc.Bytes()
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if jsonDuplicateKey(data) != nil {
				b.Fatal("duplicate")
			}
		}
	})
	b.Run("jsonc", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if jsoncDuplicateKey(data) != nil {
				b.Fatal("duplicate")
			}
		}
	})
}
