package rotini

import (
	"reflect"
	"strings"
	"testing"
)

type bigIDOutput struct {
	ID int64 `json:"id"`
}

// DecodeOutput reads integers past 2^53 back exactly, in a document and in a stream, and its
// schema check accepts them.
func TestDecodeOutput_exactIntegers(t *testing.T) {
	schema := `{"type":"object","properties":{"id":{"type":"integer","minimum":1}}}`
	one := NewProgram(Definition{Name: "app", Handler: "App",
		Output: &OutputDef{Type: reflect.TypeFor[bigIDOutput](), Schema: schema}}, nil)
	stream := NewProgram(Definition{Name: "app", Handler: "App",
		Output: &OutputDef{Type: reflect.TypeFor[bigIDOutput](), Schema: schema, Stream: true}}, nil)
	for _, format := range []string{"json", "yaml", "toml"} {
		got, err := DecodeOutput[bigIDOutput](one, []byte(map[string]string{
			"json": `{"id": 9007199254740993}`, "yaml": "id: 9007199254740993\n", "toml": "id = 9007199254740993\n",
		}[format]), format)
		if err != nil || got.ID != 9007199254740993 {
			t.Errorf("%s: ID = %d, %v; want 9007199254740993", format, got.ID, err)
		}
	}
	items, err := DecodeOutput[[]bigIDOutput](stream, []byte("{\"id\": 9007199254740993}\n{\"id\": 2}\n"), "json")
	if err != nil || len(items) != 2 || items[0].ID != 9007199254740993 {
		t.Errorf("stream = %v, %v", items, err)
	}
	if _, err := DecodeOutput[bigIDOutput](one, []byte(`{"id": 0}`), "json"); err == nil {
		t.Error("a value below the schema's minimum decoded")
	}
	if _, err := DecodeOutput[bigIDOutput](one, []byte(`{"id": 1} {"id": 2}`), "json"); err == nil {
		t.Error("trailing data after one json document decoded")
	}
}

// A partial schema drops every keyword that demands a field be present.
func TestPartialSchema_dropsPresenceKeywords(t *testing.T) {
	got, err := partialSchema([]byte(`{"dependentRequired":{"a":["b"]},"minProperties":2,` +
		`"dependencies":{"c":["d"],"e":{"required":["f"]}},` +
		`"dependentSchemas":{"g":{"required":["h"]}},"unevaluatedProperties":{"required":["i"]},` +
		`"unevaluatedItems":{"required":["j"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, kw := range []string{"dependentRequired", "minProperties", `"c"`, "required"} {
		if strings.Contains(string(got), kw) {
			t.Errorf("partial schema %s keeps %s", got, kw)
		}
	}
}
