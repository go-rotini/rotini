package shape

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sync"
	"testing"
)

// jsonData is v as encoding/json sees it: its encoding decoded into an any, numbers kept.
func jsonData(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// ptrTextByte is a byte that encodes itself through a pointer method, so encoding/json writes a
// list of them as a list, not as base64.
type ptrTextByte byte

func (b *ptrTextByte) MarshalText() ([]byte, error) { return []byte{'b', '0' + byte(*b)}, nil }

func TestExecuteByteListWithPointerTextEncoding(t *testing.T) {
	t.Parallel()
	v := struct {
		Flags []ptrTextByte `json:"flags"`
	}{Flags: []ptrTextByte{1, 2}}
	got, err := toData(reflect.ValueOf(v), 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := jsonData(t, v); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if out := execute(t, `{{join "," .flags}}`, v); out != "b1,b2" {
		t.Errorf("join = %q, want b1,b2", out)
	}
}

func TestExecuteConcurrent(t *testing.T) {
	t.Parallel()
	tmpl := mustParse(t, `{{range .tasks}}{{.id}}{{end}}`)
	render := Render[taskList](tmpl)
	data := taskList{Tasks: []task{{ID: 1}, {ID: 2}}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			var buf bytes.Buffer
			if err := render(&buf, "", data); err != nil || buf.String() != "12\n" {
				t.Errorf("got %q, %v", buf.String(), err)
			}
		})
	}
	wg.Wait()
}

// FuzzDataMatchesJSON checks that a template sees any JSON document as encoding/json decodes it.
func FuzzDataMatchesJSON(f *testing.F) {
	for _, seed := range []string{
		`{"a":1,"b":[true,null,"x"],"c":{"d":1.5e-7}}`, `[1e21,1e20,-0.000001,123456789012345678]`,
		`"s"`, `null`, `{"":{"":[]}}`, `[0.1,3.4028235e38,5e-324]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, doc string) {
		var v any
		if json.Unmarshal([]byte(doc), &v) != nil {
			return
		}
		got, err := toData(reflect.ValueOf(v), 0)
		if err != nil {
			if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
				return
			}
			t.Fatalf("%s: %v", doc, err)
		}
		if want := jsonData(t, v); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %#v\nwant %#v", doc, got, want)
		}
	})
}
