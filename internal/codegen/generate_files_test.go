package codegen

import (
	"slices"
	"testing"
)

func TestStreamNotesAndGlobRule(t *testing.T) {
	t.Parallel()
	inputs := &Inputs{
		Flags: []FlagInput{
			{Name: "output", Summary: "where to write", Schema: &InputSchema{Type: "outputfile"}},
			{Name: "input", Schema: &InputSchema{Type: "inputfile"}},
			{Name: "name", Summary: "a name", Schema: &InputSchema{Type: "string"}},
		},
		Arguments: []ArgumentInput{
			{Name: "files", Summary: "files to read", Schema: &InputSchema{Type: "[]inputfile", Glob: true}},
		},
	}
	d := buildHelpData("demo", cmdHelp{}, inputs, nil, nil, nil, "", false)
	var flags []string
	for _, f := range d.Flags {
		flags = append(flags, f.Summary)
	}
	if want := []string{"where to write (- for stdout)", "(- for stdin)", "a name"}; !slices.Equal(flags, want) {
		t.Errorf("flag summaries = %q, want %q", flags, want)
	}
	arg := d.Arguments[0]
	if arg.Summary != "files to read (- for stdin)" {
		t.Errorf("argument summary = %q", arg.Summary)
	}
	if !slices.Contains(arg.Rules, globRule) {
		t.Errorf("argument rules = %q, want the glob note", arg.Rules)
	}
}
