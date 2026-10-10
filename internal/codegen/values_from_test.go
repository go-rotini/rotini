package codegen

import (
	"slices"
	"strings"
	"testing"
)

const valuesFromSpec = `version: 0.0.0
command:
  name: taskr
  summary: track tasks
  schemas:
    Owner:
      type: object
      properties:
        login: { type: string }
    Task:
      type: object
      properties:
        title: { type: string }
        id: { type: integer }
        status: { type: string }
        owner: { $ref: "#/schemas/Owner" }
    TaskList:
      type: object
      properties:
        tasks: { type: array, items: { $ref: "#/schemas/Task" } }
        total: { type: integer }
  commands:
    - name: list
      summary: list the tasks
      output: { $ref: "#/schemas/TaskList" }
      flags:
        - name: json
          summary: write only these fields
          identifiers: [--json]
          role: fields
          schema: { type: '[]string', separator: ',', values_from: output.tasks }
        - name: sort-by
          summary: sort by this field
          identifiers: [--sort-by]
          role: sort
          schema: { type: string, values_from: output.tasks }
        - name: top
          identifiers: [--top]
          schema: { type: string, values_from: output }
        - name: owner
          identifiers: [--owner]
          schema: { type: string, values_from: output.tasks.owner }
    - name: rows
      summary: list the tasks as rows
      output: { type: array, items: { $ref: "#/schemas/Task" } }
      arguments:
        - name: field
          schema: { type: string, values_from: output }
    - name: watch
      summary: write each task
      output_stream: true
      output: { $ref: "#/schemas/Task" }
      flags:
        - name: fields
          identifiers: [--fields]
          schema: { type: '[]string', values_from: output }
`

func TestResolveValuesFrom(t *testing.T) {
	t.Parallel()
	spec := decodeSpecYAML(t, valuesFromSpec)
	enum := func(s *InputSchema) []string { return enumStrings(s.Enum) }
	task := []string{"id", "owner", "status", "title"}
	list, rows, watch := spec.Command.Commands[0], spec.Command.Commands[1], spec.Command.Commands[2]
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"envelope", enum(list.Flags[0].Schema), task},
		{"string", enum(list.Flags[1].Schema), task},
		{"root object", enum(list.Flags[2].Schema), []string{"tasks", "total"}},
		{"through an array and a $ref", enum(list.Flags[3].Schema), []string{"login"}},
		{"array root", enum(rows.Arguments[0].Schema), task},
		{"stream item", enum(watch.Flags[0].Schema), task},
	}
	for _, tt := range tests {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s: enum = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

// TestResolveValuesFrom_composedChild pins that a composed spec derives from its own output and
// schemas, since each document is normalized on its own.
func TestResolveValuesFrom_composedChild(t *testing.T) {
	t.Parallel()
	child := decodeSpecYAML(t, `version: 0.0.0
command:
  name: tasks
  summary: s
  schemas:
    Row:
      type: object
      properties:
        b: { type: string }
        a: { type: string }
  output: { type: array, items: { $ref: "#/schemas/Row" } }
  flags:
    - name: sort-by
      identifiers: [--sort-by]
      schema: { type: string, values_from: output }
`)
	if got := enumStrings(child.Command.Flags[0].Schema.Enum); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("enum = %v", got)
	}
}

func TestResolveValuesFrom_leavesProblemsToTheLint(t *testing.T) {
	t.Parallel()
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: t
  summary: s
  output: { type: object, properties: { a: { type: string } } }
  flags:
    - name: bad
      schema: { type: string, values_from: output.nope }
    - name: own
      schema: { type: string, enum: [x], values_from: output }
    - name: number
      schema: { type: int, values_from: output }
`)
	flags := spec.Command.Flags
	if len(flags[0].Schema.Enum) != 0 || !slices.Equal(enumStrings(flags[1].Schema.Enum), []string{"x"}) || len(flags[2].Schema.Enum) != 0 {
		t.Errorf("enums = %v, %v, %v", flags[0].Schema.Enum, flags[1].Schema.Enum, flags[2].Schema.Enum)
	}
}

func TestContract_valuesFrom(t *testing.T) {
	t.Parallel()
	_, commands := contractJSON(t, valuesFromSpec)
	json := entry(t, commands["taskr list"], "flags", "json")
	if json["values_from"] != "output.tasks" || json["role"] != "fields" {
		t.Errorf("json = %v", facts(json, "values_from", "role"))
	}
	items, _ := json["schema"].(map[string]any)["items"].(map[string]any)
	if got, _ := items["enum"].([]any); len(got) != 4 || got[0] != "id" {
		t.Errorf("json schema = %v, want the task fields as the items' enum", json["schema"])
	}
	if sortBy := entry(t, commands["taskr list"], "flags", "sort-by"); sortBy["role"] != "sort" {
		t.Errorf("sort-by = %v", facts(sortBy, "values_from", "role"))
	}
	if arg := entry(t, commands["taskr rows"], "arguments", "field"); arg["values_from"] != "output" {
		t.Errorf("field = %v", facts(arg, "values_from"))
	}
}

func TestHelpPage_valuesFromListsTheFields(t *testing.T) {
	dir, _ := emitModule(t, valuesFromSpec, featureConfInline)
	gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if !strings.Contains(gen, "[id|owner|status|title]") {
		t.Error("help should list the output fields as the flag's values")
	}
}
