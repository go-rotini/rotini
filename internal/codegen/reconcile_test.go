package codegen

import (
	"path/filepath"
	"slices"
	"testing"
)

// decodeSpecYAML decodes (and normalizes) a YAML spec body.
func decodeSpecYAML(t *testing.T, body string) *Spec {
	t.Helper()
	spec, err := decodeData[Spec](formatYAML, []byte(body), "test.yaml")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return spec
}

const itemsSpecHead = "version: 0.0.0\ncommand:\n  name: t\n  summary: s\n"

func TestHoistItemConstraints_everyPerValueConstraintReachesTheList(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema:
        type: array
        items:
          type: int
          enum: ["10", "20"]
          pattern: "^[0-9]+$"
          minimum: 1
          maximum: 99
          exclusiveMinimum: 0
          exclusiveMaximum: 100
          multipleOf: 10
          minLength: 1
          maxLength: 3
`)
	s := spec.Command.Flags[0].Schema
	switch {
	case !slices.Equal(s.Enum, []string{"10", "20"}):
		t.Errorf("enum = %v", s.Enum)
	case s.Pattern != "^[0-9]+$":
		t.Errorf("pattern = %q", s.Pattern)
	case bound(s.Minimum) == nil || *bound(s.Minimum) != 1:
		t.Errorf("minimum = %v", s.Minimum)
	case bound(s.Maximum) == nil || *bound(s.Maximum) != 99:
		t.Errorf("maximum = %v", s.Maximum)
	case bound(s.ExclusiveMinimum) == nil || *bound(s.ExclusiveMinimum) != 0:
		t.Errorf("exclusiveMinimum = %v", s.ExclusiveMinimum)
	case bound(s.ExclusiveMaximum) == nil || *bound(s.ExclusiveMaximum) != 100:
		t.Errorf("exclusiveMaximum = %v", s.ExclusiveMaximum)
	case bound(s.MultipleOf) == nil || *bound(s.MultipleOf) != 10:
		t.Errorf("multipleOf = %v", s.MultipleOf)
	case s.MinLength != 1 || s.MaxLength != 3:
		t.Errorf("minLength/maxLength = %d/%d", s.MinLength, s.MaxLength)
	}
}

// TestHoistItemConstraints_appliesToFlagsArgumentsEnvAndConfig pins hoisting on every
// non-stdin channel.
func TestHoistItemConstraints_appliesToFlagsArgumentsEnvAndConfig(t *testing.T) {
	item := "        type: array\n        items: { type: string, enum: [a, b] }\n"
	spec := decodeSpecYAML(t, itemsSpecHead+
		"  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n      schema:\n"+item+
		"  arguments:\n    - name: g\n      schema:\n"+item+
		"  env:\n    - name: h\n      schema:\n"+item+
		"  config:\n    - name: i\n      schema:\n"+item)

	for name, s := range map[string]*InputSchema{
		"flag":     spec.Command.Flags[0].Schema,
		"argument": spec.Command.Arguments[0].Schema,
		"env":      spec.Command.Env[0].Schema,
		"config":   spec.Command.Config[0].Schema,
	} {
		if !slices.Equal(s.Enum, []string{"a", "b"}) {
			t.Errorf("%s: enum = %v, want the items enum hoisted", name, s.Enum)
		}
	}
}

// TestHoistItemConstraints_neverOverwritesTheList pins that list-level values win over items.
func TestHoistItemConstraints_neverOverwritesTheList(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, enum: [a], minimum: 5, items: { type: string, enum: [x], minimum: 9 } }
`)
	s := spec.Command.Flags[0].Schema
	if !slices.Equal(s.Enum, []string{"a"}) || *bound(s.Minimum) != 5 {
		t.Errorf("list-level values overwritten: enum=%v minimum=%v", s.Enum, *bound(s.Minimum))
	}
}

// TestHoistItemConstraints_leavesStdinAlone pins that stdin schemas, which have full JSON
// Schema semantics, are not hoisted.
func TestHoistItemConstraints_leavesStdinAlone(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  stdin:
    format: json
    schema:
      type: array
      items: { type: string, enum: [a, b] }
`)
	if s := spec.Command.Stdin.Schema; len(s.Enum) != 0 {
		t.Errorf("stdin schema was rewritten: enum = %v", s.Enum)
	}
}

// TestHoistItemConstraints_onlyForLists pins that non-array inputs are not hoisted.
func TestHoistItemConstraints_onlyForLists(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: string, items: { type: string, enum: [a] } }
`)
	if s := spec.Command.Flags[0].Schema; len(s.Enum) != 0 {
		t.Errorf("a scalar input received an items enum: %v", s.Enum)
	}
}

// TestHoistItemConstraints_reachesComposedSpecs pins that specs decoded via readSpec (the
// $ref path) are normalized too.
func TestHoistItemConstraints_reachesComposedSpecs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "child.spec.yaml", itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, items: { type: string, enum: [a, b] } }
`)
	spec, err := readSpec(filepath.Join(dir, "child.spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if s := spec.Command.Flags[0].Schema; !slices.Equal(s.Enum, []string{"a", "b"}) {
		t.Errorf("composed spec not normalized: enum = %v", s.Enum)
	}
}

// TestHoistItemConstraints_feedsDefaultValidation pins that a default element violating an
// items-level enum is caught by lintDefaultConstraints.
func TestHoistItemConstraints_feedsDefaultValidation(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, items: { type: string, enum: [low, high] }, default: [low, BOGUS] }
`)
	if len(lintDefaultConstraints(spec)) == 0 {
		t.Error("a default element violating an items-level enum was not caught")
	}
}

// TestQualifySchemaRefs pins that bare schema refs become pointers and a command's $ref is
// untouched.
func TestQualifySchemaRefs(t *testing.T) {
	spec := &Spec{Command: Command{
		Name: "app",
		Schemas: map[string]Schema{
			"DB":   {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{"pool": {BaseSchema: BaseSchema{Ref: "Pool"}}}}},
			"Pool": {BaseSchema: BaseSchema{Type: "object"}},
		},
		Flags: []FlagInput{
			{Name: "db", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "DB"}}},
			{Name: "dbs", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Ref: "DB"}}}}},
			{Name: "qualified", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/DB"}}},
		},
		Commands: []Command{{Ref: "./child/.rotini.spec.yaml"}},
	}}
	spec.normalize()
	for got, want := range map[string]string{
		spec.Command.Flags[0].Schema.Ref:                  "#/schemas/DB",
		spec.Command.Flags[1].Schema.Items.Ref:            "#/schemas/DB",
		spec.Command.Flags[2].Schema.Ref:                  "#/schemas/DB",
		spec.Command.Schemas["DB"].Properties["pool"].Ref: "#/schemas/Pool",
		spec.Command.Commands[0].Ref:                      "./child/.rotini.spec.yaml",
	} {
		if got != want {
			t.Errorf("ref = %q, want %q", got, want)
		}
	}
}

// TestNormalizeBounds pins measured-type bound conversion, and that unitless duration bounds
// and unparseable text stay text for the lint to reject.
func TestNormalizeBounds(t *testing.T) {
	spec := &Spec{Command: Command{Name: "app", Flags: []FlagInput{
		{Name: "wait", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "duration", Minimum: "1s", Maximum: "1h30m"}}},
		{Name: "size", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "bytesize", Maximum: "1.5Gi", Minimum: float64(1024)}}},
		{Name: "bare", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "duration", Minimum: float64(5)}}},
		{Name: "bad", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "bytesize", Maximum: "lots"}}},
		{Name: "plain", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "int", Maximum: "1s"}}},
	}}}
	spec.normalize()
	f := spec.Command.Flags
	for name, tc := range map[string]struct{ got, want any }{
		"wait min": {f[0].Schema.Minimum, 1e9},
		"wait max": {f[0].Schema.Maximum, 90 * 60 * 1e9},
		"size max": {f[1].Schema.Maximum, float64(3 << 29)},
		"size min": {f[1].Schema.Minimum, float64(1024)},
		"bare":     {f[2].Schema.Minimum, "5"},
		"bad":      {f[3].Schema.Maximum, "lots"},
		"plain":    {f[4].Schema.Maximum, "1s"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#v, want %#v", name, tc.got, tc.want)
		}
	}
}

// TestInheritScalarRefConstraints pins that an input inherits a named scalar schema's unset
// constraints, its own keys win, and object schemas are not flattened.
func TestInheritScalarRefConstraints(t *testing.T) {
	spec := &Spec{Command: Command{
		Name: "app",
		Schemas: map[string]Schema{
			"Kind": {BaseSchema: BaseSchema{Type: "string", Enum: []string{"pods", "services"}, Pattern: "^[a-z]+$"}},
			"Port": {BaseSchema: BaseSchema{Type: "integer", Minimum: float64(1), Maximum: float64(65535)}},
			"DB":   {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{"host": {BaseSchema: BaseSchema{Type: "string"}}}}},
		},
		Arguments: []ArgumentInput{{Name: "kind", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "Kind"}}}},
		Flags: []FlagInput{
			{Name: "port", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "Port", Maximum: float64(1024)}}},
			{Name: "kinds", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Ref: "Kind"}}}}},
			{Name: "db", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "DB"}}},
		},
	}}
	spec.normalize()
	c := spec.Command
	if got := c.Arguments[0].Schema.Enum; !slices.Equal(got, []string{"pods", "services"}) || c.Arguments[0].Schema.Pattern != "^[a-z]+$" {
		t.Errorf("argument enum = %v, pattern = %q", got, c.Arguments[0].Schema.Pattern)
	}
	if p := c.Flags[0].Schema; *bound(p.Minimum) != 1 || *bound(p.Maximum) != 1024 {
		t.Errorf("port bounds = %v..%v, want 1..1024 (the input's own maximum wins)", p.Minimum, p.Maximum)
	}
	if got := c.Flags[1].Schema.Enum; !slices.Equal(got, []string{"pods", "services"}) {
		t.Errorf("list items did not inherit (hoisted) the enum: %v", got)
	}
	if len(c.Flags[2].Schema.Enum) != 0 || c.Flags[2].Schema.Pattern != "" {
		t.Errorf("an object ref was flattened: %+v", c.Flags[2].Schema.BaseSchema)
	}
}

// TestPatternMessage_travelsWithItsPattern pins that pattern_message is inherited with its
// pattern unless the input sets its own.
func TestPatternMessage_travelsWithItsPattern(t *testing.T) {
	spec := &Spec{Command: Command{
		Name: "app",
		Schemas: map[string]Schema{
			"Kind": {BaseSchema: BaseSchema{Type: "string", Pattern: "^[a-z]+$", PatternMessage: "must be lowercase"}},
		},
		Flags: []FlagInput{
			{Name: "kind", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "Kind"}}},
			{Name: "mine", Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "Kind", PatternMessage: "must be a kind"}}},
			{Name: "tags", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Type: "string", Pattern: "^t", PatternMessage: "must start with t"}}}}},
		},
	}}
	spec.normalize()
	for i, want := range []string{"must be lowercase", "must be a kind", "must start with t"} {
		if got := spec.Command.Flags[i].Schema.PatternMessage; got != want {
			t.Errorf("flag %s: pattern_message = %q, want %q", spec.Command.Flags[i].Name, got, want)
		}
	}
}

// TestDefinitionType_namedScalarSchema pins that a ref to a named scalar schema yields that
// schema's resolved type, so inherited constraints are checked at run time.
func TestDefinitionType_namedScalarSchema(t *testing.T) {
	schemas := map[string]Schema{
		"Kind": {BaseSchema: BaseSchema{Type: "string", Pattern: "^[a-z]+$"}},
		"Port": {BaseSchema: BaseSchema{Type: "integer"}},
		"Cfg":  {BaseSchema: BaseSchema{Type: "existingfile"}},
		"DB":   {BaseSchema: BaseSchema{Type: "object"}},
	}
	for _, tt := range []struct {
		schema *InputSchema
		want   string
	}{
		{&InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/Kind"}}, "string"},
		{&InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/Port"}}, "int"},
		{&InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/Cfg"}}, "existingfile"},
		{&InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Ref: "#/schemas/Kind"}}}}, "[]string"},
		{&InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/DB"}}, "DB"}, // an object keeps its name: it is decoded, not checked
	} {
		if got := definitionType(tt.schema, schemas); got != tt.want {
			t.Errorf("definitionType(%+v) = %q, want %q", tt.schema.BaseSchema, got, tt.want)
		}
	}
}
