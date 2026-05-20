package internal_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Root translation
// =============================================================================

func TestToProgramSpec_nilSafe(t *testing.T) {
	t.Parallel()
	got := internal.ToProgramSpec(nil)
	if got.Name != "" || got.Flags != nil || got.Commands != nil {
		t.Errorf("nil Spec: got %+v, want zero value", got)
	}
}

func TestToProgramSpec_rootName(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{Name: "greet"}
	got := internal.ToProgramSpec(s)
	if got.Name != "greet" {
		t.Errorf("Name: got %q, want %q", got.Name, "greet")
	}
}

// =============================================================================
// Flag conversion
// =============================================================================

func TestToProgramSpec_flagBasicFields(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "greet",
		Inputs: &internal.Inputs{
			Flags: []internal.Parameter{
				{
					Name:        "output",
					Identifiers: []string{"-o", "--output"},
					Schema: &internal.FieldSchema{
						Type:        "string",
						Description: "output format",
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Flags) != 1 {
		t.Fatalf("Flags: got %d, want 1", len(got.Flags))
	}
	f := got.Flags[0]
	if f.Name != "output" {
		t.Errorf("Name: got %q, want %q", f.Name, "output")
	}
	if len(f.Identifiers) != 2 || f.Identifiers[0] != "-o" || f.Identifiers[1] != "--output" {
		t.Errorf("Identifiers: got %v, want [-o --output]", f.Identifiers)
	}
	if f.Type != "string" {
		t.Errorf("Type: got %q, want %q", f.Type, "string")
	}
	if f.Description != "output format" {
		t.Errorf("Description: got %q, want %q", f.Description, "output format")
	}
}

func TestToProgramSpec_flagAutoIdentifier(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "greet",
		Inputs: &internal.Inputs{
			Flags: []internal.Parameter{
				{Name: "dry_run", Schema: &internal.FieldSchema{Type: "bool"}},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Flags) != 1 || len(got.Flags[0].Identifiers) != 1 {
		t.Fatalf("Identifiers: got %+v, want one entry", got.Flags)
	}
	if got.Flags[0].Identifiers[0] != "--dry-run" {
		t.Errorf("auto-derived identifier: got %q, want %q", got.Flags[0].Identifiers[0], "--dry-run")
	}
}

func TestToProgramSpec_jsonSchemaTypeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		schemaType string
		goType     string
	}{
		{"boolean", "bool"},
		{"integer", "int"},
		{"number", "float64"},
		{"array", "[]string"},
		{"object", "map"},
		{"duration", "time.Duration"},
		{"time", "time.Time"},
		{"datetime", "time.Time"},
		{"date", "time.Time"},
		{"string", "string"},
		{"", "string"},
		{"bool", "bool"},                     // already Go-form
		{"[]int", "[]int"},                   // already Go-form
		{"map[string]int", "map[string]int"}, // already Go-form
	}
	for _, c := range cases {
		t.Run(c.schemaType+"_to_"+c.goType, func(t *testing.T) {
			t.Parallel()
			s := &internal.Spec{
				Name: "x",
				Inputs: &internal.Inputs{
					Flags: []internal.Parameter{
						{
							Name:        "f",
							Identifiers: []string{"-f"},
							Schema:      &internal.FieldSchema{Type: c.schemaType},
						},
					},
				},
			}
			got := internal.ToProgramSpec(s)
			if got.Flags[0].Type != c.goType {
				t.Errorf("Type for %q: got %q, want %q", c.schemaType, got.Flags[0].Type, c.goType)
			}
		})
	}
}

func TestToProgramSpec_flagConstraintsFlow(t *testing.T) {
	t.Parallel()
	minVal := 1.0
	maxVal := 65535.0
	mn := 3
	mx := 32
	mi := 1
	mxi := 5
	s := &internal.Spec{
		Name: "srv",
		Inputs: &internal.Inputs{
			Flags: []internal.Parameter{
				{
					Name:        "port",
					Identifiers: []string{"--port"},
					Schema: &internal.FieldSchema{
						Type:      "integer",
						Required:  json.RawMessage("true"),
						Minimum:   &minVal,
						Maximum:   &maxVal,
						MinLength: &mn,
						MaxLength: &mx,
						MinItems:  &mi,
						MaxItems:  &mxi,
						Enum:      []string{"80", "443", "8080"},
						Pattern:   "^[0-9]+$",
						Default:   json.RawMessage(`"8080"`),
						Nullable:  true,
						Variable:  "PORT",
						Key:       "server.port",
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	f := got.Flags[0]

	if !f.Required {
		t.Errorf("Required: got false, want true")
	}
	if !f.Nullable {
		t.Errorf("Nullable: got false, want true")
	}
	if f.Default != "8080" {
		t.Errorf("Default: got %q, want %q", f.Default, "8080")
	}
	if f.Min == nil || *f.Min != minVal {
		t.Errorf("Min: got %v, want %v", f.Min, minVal)
	}
	if f.Max == nil || *f.Max != maxVal {
		t.Errorf("Max: got %v, want %v", f.Max, maxVal)
	}
	if f.MinLength == nil || *f.MinLength != mn {
		t.Errorf("MinLength: got %v, want %v", f.MinLength, mn)
	}
	if f.MaxLength == nil || *f.MaxLength != mx {
		t.Errorf("MaxLength: got %v, want %v", f.MaxLength, mx)
	}
	if f.MinItems == nil || *f.MinItems != mi {
		t.Errorf("MinItems: got %v, want %v", f.MinItems, mi)
	}
	if f.MaxItems == nil || *f.MaxItems != mxi {
		t.Errorf("MaxItems: got %v, want %v", f.MaxItems, mxi)
	}
	if len(f.Enum) != 3 || f.Enum[0] != "80" {
		t.Errorf("Enum: got %v, want [80 443 8080]", f.Enum)
	}
	if f.Pattern != "^[0-9]+$" {
		t.Errorf("Pattern: got %q, want %q", f.Pattern, "^[0-9]+$")
	}
	if f.EnvKey != "PORT" {
		t.Errorf("EnvKey: got %q, want %q", f.EnvKey, "PORT")
	}
	if f.ConfigKey != "server.port" {
		t.Errorf("ConfigKey: got %q, want %q", f.ConfigKey, "server.port")
	}
}

func TestToProgramSpec_defaultStringUnquoted(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Flags: []internal.Parameter{
				{
					Name:        "output",
					Identifiers: []string{"-o"},
					Schema: &internal.FieldSchema{
						Type:    "string",
						Default: json.RawMessage(`"yaml"`),
					},
				},
				{
					Name:        "count",
					Identifiers: []string{"-c"},
					Schema: &internal.FieldSchema{
						Type:    "integer",
						Default: json.RawMessage("42"),
					},
				},
				{
					Name:        "enabled",
					Identifiers: []string{"-e"},
					Schema: &internal.FieldSchema{
						Type:    "boolean",
						Default: json.RawMessage("true"),
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if got.Flags[0].Default != "yaml" {
		t.Errorf("string default: got %q, want %q", got.Flags[0].Default, "yaml")
	}
	if got.Flags[1].Default != "42" {
		t.Errorf("number default: got %q, want %q", got.Flags[1].Default, "42")
	}
	if got.Flags[2].Default != "true" {
		t.Errorf("bool default: got %q, want %q", got.Flags[2].Default, "true")
	}
}

func TestToProgramSpec_variableProducesEnvOnlyFlag(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Variables: []internal.Parameter{
				{
					Name: "home",
					Schema: &internal.FieldSchema{
						Type:     "string",
						Variable: "HOME",
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Flags) != 1 {
		t.Fatalf("Flags: got %d, want 1", len(got.Flags))
	}
	f := got.Flags[0]
	if !f.EnvOnly {
		t.Errorf("EnvOnly: got false, want true")
	}
	if len(f.Identifiers) != 0 {
		t.Errorf("Identifiers: got %v, want none", f.Identifiers)
	}
	if f.EnvKey != "HOME" {
		t.Errorf("EnvKey: got %q, want %q", f.EnvKey, "HOME")
	}
}

func TestToProgramSpec_fileProducesConfigFlag(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Files: []internal.Parameter{
				{
					Name: "port",
					Schema: &internal.FieldSchema{
						Type: "integer",
						File: "app",
						Key:  "server.port",
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Flags) != 1 {
		t.Fatalf("Flags: got %d, want 1", len(got.Flags))
	}
	f := got.Flags[0]
	if f.ConfigKey != "server.port" {
		t.Errorf("ConfigKey: got %q, want %q", f.ConfigKey, "server.port")
	}
	if len(f.Identifiers) != 0 {
		t.Errorf("Identifiers: got %v, want none", f.Identifiers)
	}
	if f.EnvOnly {
		t.Errorf("EnvOnly: got true, want false (config values may still be argv-shadowable in principle)")
	}
}

func TestToProgramSpec_inputOrdering(t *testing.T) {
	t.Parallel()
	// Ordering rule: flags first (in declaration order), then variables,
	// then files. This matches rotiniold convertInputs so handler-visible
	// Inputs structs list fields consistently.
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Flags: []internal.Parameter{
				{Name: "flag1", Identifiers: []string{"-1"}, Schema: &internal.FieldSchema{Type: "string"}},
				{Name: "flag2", Identifiers: []string{"-2"}, Schema: &internal.FieldSchema{Type: "string"}},
			},
			Variables: []internal.Parameter{
				{Name: "var1", Schema: &internal.FieldSchema{Type: "string", Variable: "V1"}},
			},
			Files: []internal.Parameter{
				{Name: "file1", Schema: &internal.FieldSchema{Type: "string", Key: "k1"}},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	names := make([]string, len(got.Flags))
	for i, f := range got.Flags {
		names[i] = f.Name
	}
	want := []string{"flag1", "flag2", "var1", "file1"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("order[%d]: got %q, want %q (full order: %v)", i, n, want[i], names)
		}
	}
}

// =============================================================================
// Argument conversion
// =============================================================================

func TestToProgramSpec_argBasicFields(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "greet",
		Commands: []internal.Command{
			{
				Name: "hello",
				Inputs: &internal.Inputs{
					Arguments: []internal.Parameter{
						{
							Name: "who",
							Schema: &internal.FieldSchema{
								Type:     "string",
								Required: json.RawMessage("true"),
							},
						},
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Commands) != 1 || len(got.Commands[0].Arguments) != 1 {
		t.Fatalf("Arguments: got %+v, want one", got.Commands[0].Arguments)
	}
	a := got.Commands[0].Arguments[0]
	if a.Name != "who" {
		t.Errorf("Name: got %q, want %q", a.Name, "who")
	}
	if a.Type != "string" {
		t.Errorf("Type: got %q, want %q", a.Type, "string")
	}
	if !a.Required {
		t.Errorf("Required: got false, want true")
	}
	if a.Variadic {
		t.Errorf("Variadic: got true, want false (non-array type)")
	}
}

func TestToProgramSpec_argVariadicInferred(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "cat",
		Commands: []internal.Command{
			{
				Name: "cat",
				Inputs: &internal.Inputs{
					Arguments: []internal.Parameter{
						{Name: "paths", Schema: &internal.FieldSchema{Type: "array"}},
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	a := got.Commands[0].Arguments[0]
	if !a.Variadic {
		t.Errorf("Variadic: got false, want true (array → variadic)")
	}
	if a.Type != "[]string" {
		t.Errorf("Type: got %q, want %q", a.Type, "[]string")
	}
}

// =============================================================================
// Command tree + paths
// =============================================================================

func TestToProgramSpec_singleSubcommand(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "greet",
		Commands: []internal.Command{
			{Name: "list"},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Commands) != 1 {
		t.Fatalf("Commands: got %d, want 1", len(got.Commands))
	}
	if got.Commands[0].Path != "list" {
		t.Errorf("Path: got %q, want %q", got.Commands[0].Path, "list")
	}
}

func TestToProgramSpec_threeLevelNesting(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "myprog",
		Commands: []internal.Command{
			{
				Name: "foo",
				Commands: []internal.Command{
					{
						Name: "bar",
						Commands: []internal.Command{
							{Name: "baz"},
						},
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)

	if got.Commands[0].Path != "foo" {
		t.Errorf("L1 Path: got %q, want %q", got.Commands[0].Path, "foo")
	}
	bar := got.Commands[0].Commands[0]
	if bar.Path != "foo-bar" {
		t.Errorf("L2 Path: got %q, want %q", bar.Path, "foo-bar")
	}
	baz := bar.Commands[0]
	if baz.Path != "foo-bar-baz" {
		t.Errorf("L3 Path: got %q, want %q", baz.Path, "foo-bar-baz")
	}
}

func TestToProgramSpec_commandAliasesPassThrough(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Commands: []internal.Command{
			{Name: "list", Aliases: []string{"ls", "l"}},
		},
	}
	got := internal.ToProgramSpec(s)
	if len(got.Commands[0].Aliases) != 2 {
		t.Errorf("Aliases: got %v, want [ls l]", got.Commands[0].Aliases)
	}
}

func TestToProgramSpec_commandTimeout(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Commands: []internal.Command{
			{Name: "slow", Timeout: "5s"},
			{Name: "fast"},
		},
	}
	got := internal.ToProgramSpec(s)
	if got.Commands[0].Timeout != 5*time.Second {
		t.Errorf("slow timeout: got %v, want 5s", got.Commands[0].Timeout)
	}
	if got.Commands[1].Timeout != 0 {
		t.Errorf("fast timeout (unspecified): got %v, want 0", got.Commands[1].Timeout)
	}
}

// =============================================================================
// Stdin conversion
// =============================================================================

func TestToProgramSpec_rootStdinText(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Stdin: &internal.StdinSpec{
				Schema: &internal.FieldSchema{Type: "string"},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if got.RootStdin == nil {
		t.Fatal("RootStdin: got nil, want non-nil")
	}
	if got.RootStdin.Format != "text" {
		t.Errorf("Format: got %q, want %q", got.RootStdin.Format, "text")
	}
}

func TestToProgramSpec_commandStdinJSON(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Commands: []internal.Command{
			{
				Name: "send",
				Inputs: &internal.Inputs{
					Stdin: &internal.StdinSpec{
						Schema: &internal.FieldSchema{
							Type: "object",
							Properties: map[string]*internal.FieldSchema{
								"to":      {Type: "string", Required: json.RawMessage("true")},
								"subject": {Type: "string"},
							},
						},
					},
				},
			},
		},
	}
	got := internal.ToProgramSpec(s)
	if got.Commands[0].Stdin == nil {
		t.Fatal("Stdin: got nil")
	}
	if got.Commands[0].Stdin.Format != "json" {
		t.Errorf("Format: got %q, want %q", got.Commands[0].Stdin.Format, "json")
	}
	fields := got.Commands[0].Stdin.Fields
	if len(fields) != 2 {
		t.Fatalf("Fields: got %d, want 2", len(fields))
	}
	// Alphabetical ordering.
	if fields[0].Name != "subject" || fields[1].Name != "to" {
		t.Errorf("Fields order: got %+v, want [subject, to]", fields)
	}
	for _, f := range fields {
		if f.Name == "to" && !f.Required {
			t.Errorf("to.Required: got false, want true")
		}
		if f.Name == "subject" && f.Required {
			t.Errorf("subject.Required: got true, want false")
		}
	}
}

func TestToProgramSpec_stdinNoSchemaDefaultsToJSON(t *testing.T) {
	t.Parallel()
	s := &internal.Spec{
		Name: "x",
		Inputs: &internal.Inputs{
			Stdin: &internal.StdinSpec{},
		},
	}
	got := internal.ToProgramSpec(s)
	if got.RootStdin == nil || got.RootStdin.Format != "json" {
		t.Errorf("Format: got %+v, want format=json", got.RootStdin)
	}
}

// =============================================================================
// End-to-end: round-trip from YAML through translation
// =============================================================================

func TestToProgramSpec_endToEndFromYAML(t *testing.T) {
	t.Parallel()
	yaml := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schemas/spec.json
name: todo
inputs:
  flags:
    - name: output
      identifiers: ["-o", "--output"]
      schema:
        type: string
        default: "table"
commands:
  - name: add
    inputs:
      flags:
        - name: priority
          identifiers: ["-p"]
          schema:
            type: integer
            minimum: 1
            maximum: 5
            default: 3
      arguments:
        - name: title
          schema:
            type: string
            required: true
  - name: list
    aliases: ["ls"]
    commands:
      - name: open
      - name: done
`
	path := writeSpec(t, "spec.yaml", yaml)
	s, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if err := internal.Validate(s); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ps := internal.ToProgramSpec(s)

	if ps.Name != "todo" {
		t.Errorf("Name: got %q, want %q", ps.Name, "todo")
	}
	if len(ps.Flags) != 1 || ps.Flags[0].Name != "output" {
		t.Errorf("Root flags: got %+v, want one named output", ps.Flags)
	}
	if ps.Flags[0].Default != "table" {
		t.Errorf("output.Default: got %q, want %q", ps.Flags[0].Default, "table")
	}
	if len(ps.Commands) != 2 {
		t.Fatalf("Commands: got %d, want 2", len(ps.Commands))
	}

	// add command
	add := findCmd(t, ps.Commands, "add")
	if add.Path != "add" {
		t.Errorf("add.Path: got %q, want %q", add.Path, "add")
	}
	if len(add.Flags) != 1 || add.Flags[0].Name != "priority" {
		t.Errorf("add.Flags: got %+v, want one named priority", add.Flags)
	}
	if add.Flags[0].Type != "int" {
		t.Errorf("add.priority.Type: got %q, want %q", add.Flags[0].Type, "int")
	}
	if len(add.Arguments) != 1 || !add.Arguments[0].Required {
		t.Errorf("add.title.Required: arg=%+v", add.Arguments)
	}

	// list command + subcommands
	list := findCmd(t, ps.Commands, "list")
	if len(list.Aliases) != 1 || list.Aliases[0] != "ls" {
		t.Errorf("list.Aliases: got %v, want [ls]", list.Aliases)
	}
	if len(list.Commands) != 2 {
		t.Fatalf("list.Commands: got %d, want 2", len(list.Commands))
	}
	open := findCmd(t, list.Commands, "open")
	if open.Path != "list-open" {
		t.Errorf("list-open.Path: got %q, want %q", open.Path, "list-open")
	}
	done := findCmd(t, list.Commands, "done")
	if done.Path != "list-done" {
		t.Errorf("list-done.Path: got %q, want %q", done.Path, "list-done")
	}
}

// findCmd locates the named command in a [rtk.CommandSpec] slice or
// fails the test.
func findCmd(t *testing.T, cmds []rtk.CommandSpec, name string) *rtk.CommandSpec {
	t.Helper()
	for i := range cmds {
		if cmds[i].Name == name {
			return &cmds[i]
		}
	}
	t.Fatalf("command %q not found among %d commands", name, len(cmds))
	return nil
}
