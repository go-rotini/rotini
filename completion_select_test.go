package rotini

import (
	"slices"
	"testing"
)

// fieldsDef has a separated list flag and argument whose enum is a set of output fields, as
// `values_from` derives them.
func fieldsDef() Definition {
	fields := []string{"id", "status", "title"}
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "json", Identifiers: []string{"--json"}, Type: "[]string", Separator: ",", Enum: fields},
			{Name: "sort-by", Identifiers: []string{"--sort-by"}, Type: "string", Enum: fields},
			{Name: "described", Identifiers: []string{"--described"}, Type: "[]string", Separator: ":", Enum: []string{"a", "b"},
				EnumValues: []EnumValue{{Value: "a", Summary: "the a"}, {Value: "b", Summary: "the b"}}},
		},
		Arguments: []ArgDef{{Name: "cols", Type: "[]string", Variadic: true, Separator: ",", Enum: fields}},
	}
}

// TestComplete_separatedEnum pins that a separated enum list completes the item after the last
// separator, repeats what was typed before it, leaves out values already listed, and asks for
// no space; a single-value enum still gets a space.
func TestComplete_separatedEnum(t *testing.T) {
	t.Parallel()
	p := NewProgram(fieldsDef(), nil)
	tests := []struct {
		words   []string
		want    []string
		noSpace bool
	}{
		{[]string{"--json", ""}, []string{"id", "status", "title"}, true},
		{[]string{"--json", "id,"}, []string{"id,status", "id,title"}, true},
		{[]string{"--json", "id,t"}, []string{"id,title"}, true},
		{[]string{"--json", "id,title,"}, []string{"id,title,status"}, true},
		{[]string{"--json", "id,status,title,"}, nil, false},
		{[]string{"--json=id,"}, []string{"--json=id,status", "--json=id,title"}, true},
		{[]string{"--sort-by", ""}, []string{"id", "status", "title"}, false},
		{[]string{"title,"}, []string{"title,id", "title,status"}, true},
		{[]string{"--described", "a:"}, []string{"a:b"}, true},
	}
	for _, tt := range tests {
		r := resultOf(t, p, tt.words...)
		if !slices.Equal(values(r), tt.want) || r.NoSpace != tt.noSpace {
			t.Errorf("%q: %q nospace=%v, want %q nospace=%v", tt.words, values(r), r.NoSpace, tt.want, tt.noSpace)
		}
	}
	r := resultOf(t, p, "--described", "a:")
	if len(r.Candidates) != 1 || r.Candidates[0].Description != "the b" {
		t.Errorf("--described a: = %+v, want a:b with its summary", r.Candidates)
	}
}
