package codegen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestConstraintRendering pins the exact output of constraintTags and constraintFields.
func TestConstraintRendering(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := &InputSchema{
		Minimum: f(1), Maximum: f(10),
		ExclusiveMinimum: f(0), ExclusiveMaximum: f(100),
		MultipleOf: f(2),
		MinLength:  3, MaxLength: new(20), MinItems: 1, MaxItems: new(5),
		Pattern: "^x$"}
	const wantTags = `min:"1" max:"10" xmin:"0" xmax:"100" multipleof:"2" minlen:"3" maxlen:"20" minitems:"1" maxitems:"5" pattern:"^x$"`
	const wantLit = `Minimum: new(float64(1)), Maximum: new(float64(10)), ExclusiveMinimum: new(float64(0)), ExclusiveMaximum: new(float64(100)), MultipleOf: new(float64(2)), MinLength: 3, MaxLength: new(20), MinItems: 1, MaxItems: new(5), Pattern: "^x$"`
	if got := constraintTags(s); got != wantTags {
		t.Errorf("constraintTags:\n got=%s\nwant=%s", got, wantTags)
	}
	if got := constraintFields(s); got != wantLit {
		t.Errorf("constraintFields:\n got=%s\nwant=%s", got, wantLit)
	}
	if got := constraintTags(&InputSchema{}); got != "" {
		t.Errorf("constraintTags(empty) = %q, want empty", got)
	}
	if got := constraintFields(&InputSchema{}); got != "" {
		t.Errorf("constraintFields(empty) = %q, want empty", got)
	}
}

// TestDefinitionTypePreservesParserSemantics pins the split between the Definition's type
// string (which keeps parser-significant names) and the generated field's Go type.
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
		{"input file", &InputSchema{Type: "inputfile"}, "inputfile", "string"},
		{"output file", &InputSchema{Type: "outputfile"}, "outputfile", "string"},
		{"list of input files", &InputSchema{Type: "[]inputfile"}, "[]inputfile", "[]string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := definitionType(tt.schema, nil); got != tt.wantDefType {
				t.Errorf("definitionType = %q, want %q", got, tt.wantDefType)
			}
			if got := goFieldType(tt.schema); got != tt.wantFieldType {
				t.Errorf("goFieldType = %q, want %q", got, tt.wantFieldType)
			}
		})
	}
}

// TestDefinitionTypeReachesEmittedLiterals pins that the FlagDef and ArgDef emitters use
// definitionType.
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
	got := flagDefsLiteral(in, nil, false) + argDefsLiteral(in, nil)
	for _, want := range []string{`Type: "existingfile"`, `Type: "count"`, `Type: "existingdir"`} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted literals missing %s\ngot: %s", want, got)
		}
	}
}

// TestFlagDefsLiteral_objectFlags pins that an object flag carries its ObjectSchema and a
// JSON-encoded default (one document per element for a list).
func TestFlagDefsLiteral_objectFlags(t *testing.T) {
	schemas := map[string]Schema{
		"DB": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{
			"host": {BaseSchema: BaseSchema{Type: "string"}},
			"pool": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{"max": {BaseSchema: BaseSchema{Type: "integer"}}}}},
		}}},
		"Label": {BaseSchema: BaseSchema{Type: "string"}}, // a named scalar: not an object flag
	}
	in := &Inputs{Flags: []FlagInput{
		{Name: "db", Identifiers: []string{"--db"}, Schema: &InputSchema{Ref: "#/schemas/DB",
			Default: map[string]any{"host": "a, b", "pool": map[string]any{"max": 3}}}},
		{Name: "dbs", Identifiers: []string{"--dbs"}, Schema: &InputSchema{Type: "array", Items: &Schema{Ref: "#/schemas/DB"},
			Default: []any{map[string]any{"host": "x"}, map[string]any{"host": "y"}}}},
		{Name: "label", Identifiers: []string{"--label"}, Schema: &InputSchema{Ref: "#/schemas/Label"}},
	}}
	got := flagDefsLiteral(in, schemas, false)
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

// TestLayoutFor pins that `date` gets Layout "2006-01-02" (on the definition for argv, as a
// struct tag for env and config) and that an explicit `layout:` wins.
func TestLayoutFor(t *testing.T) {
	for _, tc := range []struct {
		schema *InputSchema
		want   string
	}{
		{&InputSchema{Type: "date"}, "2006-01-02"},
		{&InputSchema{Type: "[]date"}, "2006-01-02"},
		{&InputSchema{Type: "array", Items: &Schema{Type: "date"}}, "2006-01-02"},
		{&InputSchema{Type: "date", Layout: "02/01/2006"}, "02/01/2006"},
		{&InputSchema{Type: "datetime"}, ""},
		{&InputSchema{Type: "time", Layout: "unix"}, "unix"},
		{nil, ""},
	} {
		if got := layoutFor(tc.schema); got != tc.want {
			t.Errorf("layoutFor(%+v) = %q, want %q", tc.schema, got, tc.want)
		}
	}
	in := &Inputs{
		Flags:     []FlagInput{{Name: "due", Identifiers: []string{"--due"}, Schema: &InputSchema{Type: "date"}}},
		Arguments: []ArgumentInput{{Name: "at", Schema: &InputSchema{Type: "time", Layout: "unix"}}},
	}
	if lit := flagDefsLiteral(in, nil, false) + argDefsLiteral(in, nil); !strings.Contains(lit, `Layout: "2006-01-02"`) || !strings.Contains(lit, `Layout: "unix"`) {
		t.Errorf("literal does not carry the layouts:\n%s", lit)
	}
	if tags := constraintTags(&InputSchema{Type: "date"}); tags != `layout:"2006-01-02"` {
		t.Errorf("channel tags = %q", tags)
	}
}

// TestDefsLiteral_deprecatedMessages pins that `deprecated:` messages reach the flag, argument,
// and command literals.
func TestFlagDefsLiteral_shortCircuit(t *testing.T) {
	in := &Inputs{Flags: []FlagInput{
		{Name: "help", Identifiers: []string{"--help"}, ShortCircuit: true, Schema: &InputSchema{Type: "bool"}},
		{Name: "verbose", Identifiers: []string{"--verbose"}, Schema: &InputSchema{Type: "bool"}},
	}}
	lit := flagDefsLiteral(in, nil, false)
	if n := strings.Count(lit, "ShortCircuit: true"); n != 1 {
		t.Errorf("literal has %d ShortCircuit: true, want exactly 1 (only --help):\n%s", n, lit)
	}
}

// TestFlagDefsLiteral_local pins that only a non-cascading flag of a command with sub-commands
// is marked Local; a leaf's flags have no sub-commands to keep them from.
func TestFlagDefsLiteral_local(t *testing.T) {
	in := &Inputs{Flags: []FlagInput{
		{Name: "help", Identifiers: []string{"--help"}, Cascading: true, Schema: &InputSchema{Type: "bool"}},
		{Name: "init", Identifiers: []string{"--init"}, Schema: &InputSchema{Type: "bool"}},
	}}
	if lit := flagDefsLiteral(in, nil, true); strings.Count(lit, "Local: true") != 1 || !strings.Contains(lit, `Name: "init", Identifiers: []string{"--init"}, Type: "bool", Local: true`) {
		t.Errorf("branch literal marks the wrong flags Local:\n%s", lit)
	}
	if lit := flagDefsLiteral(in, nil, false); strings.Contains(lit, "Local") {
		t.Errorf("leaf literal marks a flag Local:\n%s", lit)
	}
}

func TestDefsLiteral_deprecatedMessages(t *testing.T) {
	in := &Inputs{
		Flags:     []FlagInput{{Name: "conf", Identifiers: []string{"--conf"}, Deprecated: "use --config"}},
		Arguments: []ArgumentInput{{Name: "legacy", Deprecated: "no longer read"}},
	}
	lit := flagDefsLiteral(in, nil, false) + argDefsLiteral(in, nil)
	for _, want := range []string{`Deprecated: "use --config"`, `Deprecated: "no longer read"`} {
		if !strings.Contains(lit, want) {
			t.Errorf("literal missing %s:\n%s", want, lit)
		}
	}
	cmds := rnodesLiteral("app", "app", []rnode{{name: "build", prefix: "AppBuild", deprecated: "use app make"}}, nil)
	if !strings.Contains(cmds, `Deprecated: "use app make"`) {
		t.Errorf("command literal missing its message:\n%s", cmds)
	}
}

// A config_source flag's own environment fallback supplies the path, ahead of an env input's
// variable.
func TestInputSettings_configSourceVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, inputs, want string
	}{
		{"the flag's variable", `
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, variable: ACME_CONFIG, config_source: user}}`,
			`PathFrom: &rotini.PathFromDef{Flag: "config", Env: "ACME_CONFIG"}`},
		{"the flag's variables, then the env input's", `
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, variable: [ACME_CONFIG, ACME_CFG], config_source: user}}
  env:
    - {name: config_path, schema: {type: string, config_source: user}}`,
			`PathFrom: &rotini.PathFromDef{Flag: "config", Env: "ACME_CONFIG,ACME_CFG,ACME_CONFIG_PATH"}`},
		{"a variable both name is listed once", `
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, variable: ACME_CONFIG, config_source: user}}
  env:
    - {name: config, schema: {type: string, variable: ACME_CONFIG, config_source: user}}`,
			`PathFrom: &rotini.PathFromDef{Flag: "config", Env: "ACME_CONFIG"}`},
		{"a flag with no variable", `
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, config_source: user}}`,
			`PathFrom: &rotini.PathFromDef{Flag: "config"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := "version: 0.0.0\ncommand:\n  name: acme\n  env_prefix: ACME\n  config_files:\n    - {name: user, path: ~/.acme.yaml}" + tt.inputs + "\n"
			gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/acme")
			if err != nil {
				t.Fatal(err)
			}
			if got := renderInputSettings(gp); !strings.Contains(got, tt.want) {
				t.Errorf("settings lack %s:\n%s", tt.want, got)
			}
		})
	}
}

// A config_source flag's path names a config file, so neither help nor the contract gives it a
// config key; its variable still shows.
func TestConfigSourceFlag_hasNoConfigKey(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: acme
  config_files:
    - {name: user, path: ~/.acme.yaml}
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, variable: ACME_CONFIG, config_source: user}}
    - {name: region, identifiers: [--region], schema: {type: string, variable: ACME_REGION}}
`
	_, commands := contractJSON(t, spec)
	for _, f := range commands["acme"]["flags"].([]any) {
		f := f.(map[string]any)
		switch f["name"] {
		case "config":
			if f["config_key"] != nil {
				t.Errorf("contract: --config has config_key %v", f["config_key"])
			}
		case "region":
			if f["config_key"] != "region" {
				t.Errorf("contract: --region has config_key %v, want region", f["config_key"])
			}
		}
	}
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/acme")
	if err != nil {
		t.Fatal(err)
	}
	rows := buildHelpData("acme", gp.rootHelp, gp.rootInputs, gp.tree, nil, nil, gp.envPrefix, gp.configReadsOf(nil)).Flags
	row := rowFor(t, rows, "--config")
	if row.ConfigKey != "" || !slices.Equal(row.Env, []string{"ACME_CONFIG"}) {
		t.Errorf("help: --config has Env %v, ConfigKey %q; want [ACME_CONFIG] and no key", row.Env, row.ConfigKey)
	}
}
