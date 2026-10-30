package codegen

import (
	"reflect"
	"testing"
)

// TestToolBounds_durationAndBytesize pins that a tool parameter leaves out the bounds of a
// duration or bytesize value, which the contract writes as nanoseconds or bytes on a string,
// while the contract keeps them for `rotini diff`.
func TestToolBounds_durationAndBytesize(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		schema *InputSchema
	}{
		{"duration", &InputSchema{Type: "duration", Minimum: "1s", Maximum: "1h"}},
		{"bytesize", &InputSchema{Type: "bytesize", ExclusiveMinimum: "0B", Maximum: "10MB"}},
		{"[]duration", &InputSchema{Type: "[]duration", Minimum: "1s"}},
	} {
		contract, _ := inputJSONSchema(tc.schema).(map[string]any)
		bounds := contract
		if items, ok := contract["items"].(map[string]any); ok {
			bounds = items
		}
		if bounds["minimum"] == nil && bounds["maximum"] == nil {
			t.Errorf("%s: the contract schema %v lost its bounds", tc.typ, contract)
		}
		var tl tool
		tl.properties = map[string]any{}
		tl.addParam(agentInput{Name: "v", Type: tc.typ, Schema: contract})
		got := tl.properties["v"].(map[string]any)
		want := map[string]any{"type": "string"}
		if _, ok := got["items"]; ok {
			want = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tool parameter = %v, want %v", tc.typ, got, want)
		}
		if bounds["minimum"] == nil && bounds["maximum"] == nil {
			t.Errorf("%s: stripping the tool's bounds changed the contract's: %v", tc.typ, contract)
		}
	}

	// Other numeric bounds stay.
	var tl tool
	tl.properties = map[string]any{}
	tl.addParam(agentInput{Name: "n", Type: "int", Schema: map[string]any{"type": "integer", "minimum": 1}})
	if tl.properties["n"].(map[string]any)["minimum"] != 1 {
		t.Errorf("an int parameter lost its minimum: %v", tl.properties["n"])
	}
}
