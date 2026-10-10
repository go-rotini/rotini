package rotini

import (
	"bytes"
	"slices"
	"testing"
)

// outFieldsDef is fieldsDef's list flag on a command whose output is outList, with the enum
// `values_from: output.tasks` derives for it.
func outFieldsDef() Definition {
	fields := []string{"id", "status"}
	return Definition{
		Name: "app", Handler: "App",
		Output: outDef().Commands[0].Output,
		Flags: []FlagDef{
			{Name: "json", Identifiers: []string{"--json"}, Type: "[]string", Separator: ",", Enum: fields},
			{Name: "sort-by", Identifiers: []string{"--sort-by"}, Type: "string", Enum: fields},
		},
	}
}

type outFieldsInputs struct {
	App struct {
		Flags struct {
			JSON   []string `rotini:"json"`
			SortBy string   `rotini:"sort-by"`
		}
		Arguments struct{}
	}
}

// outputConformanceCases are the field selection rows of the conformance matrix: an output
// field list read from the command line, written as a selection, sorted, and completed.
func outputConformanceCases() []dataCase {
	return []dataCase{
		{"OUT-01", func(t *testing.T) { // a field list binds its items; a name off the output is a usage error
			in, err := NewContextFor(outFieldsDef(), []string{"--json", "status,id", "--sort-by", "id"}).Inputs[outFieldsInputs]()
			if err != nil || !slices.Equal(in.App.Flags.JSON, []string{"status", "id"}) || in.App.Flags.SortBy != "id" {
				t.Errorf("inputs = %+v, %v", in.App.Flags, err)
			}
			_, err = NewContextFor(outFieldsDef(), []string{"--json", "id,nope"}).Inputs[outFieldsInputs]()
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("err = %v, want a usage error", err)
			}
		}},
		{"OUT-02", func(t *testing.T) { // a selection writes the chosen fields and passes output checks
			var out bytes.Buffer
			rtx := NewContextFor(outFieldsDef(), nil).WithStdout(&out)
			rtx.WithOutputChecks(true)
			sel, err := SelectFields(sampleList, "tasks", []string{"id"})
			if err == nil {
				err = rtx.WriteOutput(sel, "json", nil)
			}
			if err != nil || out.String() != "{\n  \"tasks\": [\n    {\n      \"id\": 1\n    },\n    {\n      \"id\": 2\n    }\n  ]\n}\n" {
				t.Errorf("wrote %q, %v", out.String(), err)
			}
		}},
		{"OUT-03", func(t *testing.T) { // SortBy orders items by a field, stably, missing values last
			items := []selTask{{ID: 2, Status: "open"}, {ID: 1}, {ID: 3, Status: "done"}}
			if err := SortBy(items, "status", false); err != nil || items[0].ID != 3 || items[1].ID != 2 || items[2].ID != 1 {
				t.Errorf("sorted %+v, %v", items, err)
			}
		}},
		{"OUT-04", func(t *testing.T) { // a field list completes the item after its last separator
			r := resultOf(t, NewProgram(outFieldsDef(), nil), "--json", "id,")
			if !slices.Equal(values(r), []string{"id,status"}) || !r.NoSpace {
				t.Errorf("candidates %q nospace=%v", values(r), r.NoSpace)
			}
		}},
	}
}

// TestConformance_OutputMatrix runs the field selection rows of the conformance matrix. Their
// IDs are in the canonical list TestConformance_matrixComplete checks.
func TestConformance_OutputMatrix(t *testing.T) {
	for _, c := range outputConformanceCases() {
		t.Run(c.id, c.check)
	}
}
