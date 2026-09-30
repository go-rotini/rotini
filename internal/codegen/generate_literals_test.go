package codegen

import (
	"strings"
	"testing"
)

// TestConstraintRendering pins the EXACT struct-tag + Go-literal output of
// constraintTags/constraintsLiteral before they are deduped behind eachConstraint —
// the emitted bytes (not just compilation) must stay identical.
func TestConstraintRendering(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := &InputSchema{
		Minimum: f(1), Maximum: f(10),
		ExclusiveMinimum: f(0), ExclusiveMaximum: f(100),
		MultipleOf: f(2),
		MinLength:  3, MaxLength: 20, MinItems: 1, MaxItems: 5,
		Pattern: "^x$"}
	const wantTags = `min:"1" max:"10" xmin:"0" xmax:"100" multipleof:"2" minlen:"3" maxlen:"20" minitems:"1" maxitems:"5" pattern:"^x$"`
	const wantLit = `rotini.Constraints{Minimum: rotini.Ptr[float64](1), Maximum: rotini.Ptr[float64](10), ExclusiveMinimum: rotini.Ptr[float64](0), ExclusiveMaximum: rotini.Ptr[float64](100), MultipleOf: rotini.Ptr[float64](2), MinLength: 3, MaxLength: 20, MinItems: 1, MaxItems: 5, Pattern: "^x$"}`
	if got := constraintTags(s); got != wantTags {
		t.Errorf("constraintTags:\n got=%s\nwant=%s", got, wantTags)
	}
	if got := constraintsLiteral(s); got != wantLit {
		t.Errorf("constraintsLiteral:\n got=%s\nwant=%s", got, wantLit)
	}
	if got := constraintTags(&InputSchema{}); got != "" {
		t.Errorf("constraintTags(empty) = %q, want empty", got)
	}
	if got := constraintsLiteral(&InputSchema{}); got != "" {
		t.Errorf("constraintsLiteral(empty) = %q, want empty", got)
	}
}

// TestDefinitionTypePreservesParserSemantics pins the split between the Definition's type
// string and the generated field's Go type.
//
// This exists because it was broken in exactly the way a unit test could not see: parser_test
// hand-builds a Definition with Type "existingfile" and passes, while every CODEGEN path ran
// the name through jsonSchemaTypeToGo and emitted "string", so the parse-time existence check
// never fired for a real CLI. The e2e tier now covers the behavior end to end; this covers the
// seam itself, so a future "simplify" that collapses the two back together fails here first.
func TestDefinitionTypePreservesParserSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		schema        *InputSchema
		wantDefType   string
		wantFieldType string
	}{
		{"count", &InputSchema{Type: "count"}, "count", "int"},
		{"existing file", &InputSchema{Type: "existingfile"}, "existingfile", "string"},
		{"existing dir", &InputSchema{Type: "existingdir"}, "existingdir", "string"},
		{"array of existing files", &InputSchema{Type: "array", Items: &Schema{Type: "existingfile"}}, "[]existingfile", "[]string"},
		{"plain string is unaffected", &InputSchema{Type: "string"}, "string", "string"},
		{"go names still map", &InputSchema{Type: "integer"}, "int", "int"},
		{"array of strings is unaffected", &InputSchema{Type: "array", Items: &Schema{Type: "string"}}, "[]string", "[]string"},
		{"a $ref wins over the type name", &InputSchema{Ref: "#/schemas/Meta", Type: "count"}, "Meta", "Meta"},
		{"nil schema defaults to string", nil, "string", "string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := definitionType(tt.schema); got != tt.wantDefType {
				t.Errorf("definitionType = %q, want %q", got, tt.wantDefType)
			}
			if got := goFieldType(tt.schema); got != tt.wantFieldType {
				t.Errorf("goFieldType = %q, want %q", got, tt.wantFieldType)
			}
		})
	}
}

// TestDefinitionTypeReachesEmittedLiterals walks the same types through the actual FlagDef and
// ArgDef emitters, because the helper being right is worth nothing if a call site skips it —
// which is precisely how the original defect survived.
func TestDefinitionTypeReachesEmittedLiterals(t *testing.T) {
	t.Parallel()
	in := &Inputs{
		Flags: []FlagInput{
			{Name: "config", Schema: &InputSchema{Type: "existingfile"}},
			{Name: "verbose", Schema: &InputSchema{Type: "count"}},
		},
		Arguments: []ArgumentInput{
			{Name: "dir", Schema: &InputSchema{Type: "existingdir"}},
		},
	}
	got := flagDefsLiteral(in, nil) + argDefsLiteral(in)
	for _, want := range []string{`Type: "existingfile"`, `Type: "count"`, `Type: "existingdir"`} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted literals missing %s\ngot: %s", want, got)
		}
	}
}

// An object flag's definition carries the named schema it is validated against, and its default
// as the JSON document the flag decodes — one per element for a list. The generic rendering would
// turn a mapping into key=value occurrences, which loses nesting and splits a value on its commas.
func TestFlagDefsLiteral_objectFlags(t *testing.T) {
	schemas := map[string]Schema{
		"DB": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{
			"host": {BaseSchema: BaseSchema{Type: "string"}},
			"pool": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{"max": {BaseSchema: BaseSchema{Type: "integer"}}}}},
		}}},
		"Label": {BaseSchema: BaseSchema{Type: "string"}}, // a named scalar: not an object flag
	}
	in := &Inputs{Flags: []FlagInput{
		{Name: "db", Identifiers: []string{"--db"}, Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/DB"},
			Default: map[string]any{"host": "a, b", "pool": map[string]any{"max": 3}}}},
		{Name: "dbs", Identifiers: []string{"--dbs"}, Schema: &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Ref: "#/schemas/DB"}}},
			Default: []any{map[string]any{"host": "x"}, map[string]any{"host": "y"}}}},
		{Name: "label", Identifiers: []string{"--label"}, Schema: &InputSchema{BaseSchema: BaseSchema{Ref: "#/schemas/Label"}}},
	}}
	got := flagDefsLiteral(in, schemas)
	for _, want := range []string{
		`Default: "{\"host\":\"a, b\",\"pool\":{\"max\":3}}"`,
		`Defaults: []string{"{\"host\":\"x\"}", "{\"host\":\"y\"}"}`,
		`ObjectSchema: `,
		`#/definitions/DB`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("literal missing %s:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "ObjectSchema:"); n != 2 {
		t.Errorf("%d flags carry ObjectSchema, want 2 (the named scalar is not an object):\n%s", n, got)
	}
}

// `date` means a calendar date, so it carries Layout "2006-01-02" to the runtime — on the
// definition for argv, and as a struct tag for env and config — and an explicit `layout:` wins.
func TestLayoutFor(t *testing.T) {
	for _, tc := range []struct {
		schema *InputSchema
		want   string
	}{
		{&InputSchema{BaseSchema: BaseSchema{Type: "date"}}, "2006-01-02"},
		{&InputSchema{BaseSchema: BaseSchema{Type: "[]date"}}, "2006-01-02"},
		{&InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: BaseSchema{Type: "date"}}}}, "2006-01-02"},
		{&InputSchema{BaseSchema: BaseSchema{Type: "date"}, Layout: "02/01/2006"}, "02/01/2006"},
		{&InputSchema{BaseSchema: BaseSchema{Type: "datetime"}}, ""},
		{&InputSchema{BaseSchema: BaseSchema{Type: "time"}, Layout: "unix"}, "unix"},
		{nil, ""},
	} {
		if got := layoutFor(tc.schema); got != tc.want {
			t.Errorf("layoutFor(%+v) = %q, want %q", tc.schema, got, tc.want)
		}
	}
	in := &Inputs{
		Flags:     []FlagInput{{Name: "due", Identifiers: []string{"--due"}, Schema: &InputSchema{BaseSchema: BaseSchema{Type: "date"}}}},
		Arguments: []ArgumentInput{{Name: "at", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "time"}, Layout: "unix"}}},
	}
	if lit := flagDefsLiteral(in, nil) + argDefsLiteral(in); !strings.Contains(lit, `Layout: "2006-01-02"`) || !strings.Contains(lit, `Layout: "unix"`) {
		t.Errorf("literal does not carry the layouts:\n%s", lit)
	}
	if tags := constraintTags(&InputSchema{BaseSchema: BaseSchema{Type: "date"}}); tags != `layout:"2006-01-02"` {
		t.Errorf("channel tags = %q", tags)
	}
}
