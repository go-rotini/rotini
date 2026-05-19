package internal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// =============================================================================
// Format round-trip: write per format → read → verify fields
// =============================================================================

// minimalYAML is a small spec that exercises the most common DSL constructs:
// a root flag (under inputs.flags), a sub-command, and a per-command flag.
// Each format-specific test writes the equivalent content in that format
// and verifies the loader produces a Spec that round-trips.
const minimalYAML = `name: greet
inputs:
  flags:
    - name: verbose
      identifiers: ["-v", "--verbose"]
      schema:
        type: bool
commands:
  - name: hello
    inputs:
      arguments:
        - name: who
          schema:
            type: string
            required: true
`

const minimalJSON = `{
  "name": "greet",
  "inputs": {
    "flags": [
      {
        "name": "verbose",
        "identifiers": ["-v", "--verbose"],
        "schema": {"type": "bool"}
      }
    ]
  },
  "commands": [
    {
      "name": "hello",
      "inputs": {
        "arguments": [
          {
            "name": "who",
            "schema": {"type": "string", "required": true}
          }
        ]
      }
    }
  ]
}`

const minimalJSONC = `{
  // root command name
  "name": "greet",
  "inputs": {
    "flags": [
      {
        "name": "verbose",
        "identifiers": ["-v", "--verbose"],
        "schema": {"type": "bool"}
      }
    ]
  },
  "commands": [
    {
      "name": "hello",
      "inputs": {
        "arguments": [
          {"name": "who", "schema": {"type": "string", "required": true}}
        ]
      }
    }
  ]
}`

const minimalTOML = `
name = "greet"

[[inputs.flags]]
name = "verbose"
identifiers = ["-v", "--verbose"]

[inputs.flags.schema]
type = "bool"

[[commands]]
name = "hello"

[[commands.inputs.arguments]]
name = "who"

[commands.inputs.arguments.schema]
type = "string"
required = true
`

func writeSpec(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadSpec_yaml(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.yaml", minimalYAML)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	assertMinimalSpec(t, got)
}

func TestLoadSpec_yml(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.yml", minimalYAML)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	assertMinimalSpec(t, got)
}

func TestLoadSpec_json(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.json", minimalJSON)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	assertMinimalSpec(t, got)
}

func TestLoadSpec_jsonc(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.jsonc", minimalJSONC)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	assertMinimalSpec(t, got)
}

func TestLoadSpec_toml(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.toml", minimalTOML)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	assertMinimalSpec(t, got)
}

// TestLoadSpec_jsonWithComments verifies the content-sniff fallback: a
// .json file with line comments is parsed via the jsonc parser.
func TestLoadSpec_jsonWithComments(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.json", minimalJSONC) // .json extension, jsonc content
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec (jsonc fallback): %v", err)
	}
	assertMinimalSpec(t, got)
}

// assertMinimalSpec verifies the fields the [minimal*] fixtures declare.
func assertMinimalSpec(t *testing.T, got *internal.Spec) {
	t.Helper()
	if got.Name != "greet" {
		t.Errorf("Name: got %q, want %q", got.Name, "greet")
	}
	if got.Inputs == nil || len(got.Inputs.Flags) != 1 {
		t.Fatalf("Inputs.Flags: got %+v, want one flag", got.Inputs)
	}
	flag := got.Inputs.Flags[0]
	if flag.Name != "verbose" {
		t.Errorf("flag name: got %q, want %q", flag.Name, "verbose")
	}
	if len(flag.Identifiers) != 2 || flag.Identifiers[0] != "-v" || flag.Identifiers[1] != "--verbose" {
		t.Errorf("flag identifiers: got %v, want [-v --verbose]", flag.Identifiers)
	}
	if flag.Schema == nil || flag.Schema.Type != "bool" {
		t.Errorf("flag schema: got %+v, want type=bool", flag.Schema)
	}

	if len(got.Commands) != 1 {
		t.Fatalf("Commands: got %d, want 1", len(got.Commands))
	}
	cmd := got.Commands[0]
	if cmd.Name != "hello" {
		t.Errorf("cmd name: got %q, want %q", cmd.Name, "hello")
	}
	if cmd.Inputs == nil || len(cmd.Inputs.Arguments) != 1 {
		t.Fatalf("cmd.Inputs.Arguments: got %+v, want one arg", cmd.Inputs)
	}
	arg := cmd.Inputs.Arguments[0]
	if arg.Name != "who" {
		t.Errorf("arg name: got %q, want %q", arg.Name, "who")
	}
	if arg.Schema == nil || arg.Schema.Type != "string" {
		t.Errorf("arg schema: got %+v, want type=string", arg.Schema)
	}
	if !arg.Schema.RequiredBool() {
		t.Errorf("arg.Schema.RequiredBool(): got false, want true")
	}
}

// =============================================================================
// Error cases
// =============================================================================

func TestLoadSpec_unknownField(t *testing.T) {
	t.Parallel()
	content := `name: greet
typo: nope
`
	path := writeSpec(t, "spec.yaml", content)
	_, err := internal.LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), `"typo"`) {
		t.Errorf("error: got %q, want mention of unknown field 'typo'", err.Error())
	}
}

func TestLoadSpec_unsupportedExtension(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.ini", "name = greet")
	_, err := internal.LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for unsupported extension, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported file extension") {
		t.Errorf("error: got %q, want mention of 'unsupported file extension'", err.Error())
	}
}

func TestLoadSpec_malformedYAML(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.yaml", "name: greet\n  bad: indentation\nname: dup\n")
	_, err := internal.LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoadSpec_malformedJSON(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "spec.json", `{"name": "greet"`)
	_, err := internal.LoadSpec(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestLoadSpec_nonexistentFile(t *testing.T) {
	t.Parallel()
	_, err := internal.LoadSpec(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// =============================================================================
// Constraint round-trip — all the schema fields the rtk runtime cares about
// =============================================================================

func TestLoadSpec_allConstraintFields(t *testing.T) {
	t.Parallel()
	content := `name: srv
inputs:
  flags:
    - name: port
      identifiers: ["--port"]
      schema:
        type: int
        minimum: 1
        maximum: 65535
        required: true
    - name: name
      identifiers: ["--name"]
      schema:
        type: string
        minLength: 3
        maxLength: 32
        pattern: "^[a-z][a-z0-9-]*$"
        enum: ["alpha", "beta"]
        default: "alpha"
    - name: tags
      identifiers: ["--tags"]
      schema:
        type: "[]string"
        minItems: 1
        maxItems: 10
        nullable: true
`
	path := writeSpec(t, "spec.yaml", content)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if got.Inputs == nil || len(got.Inputs.Flags) != 3 {
		t.Fatalf("Flags: got %d, want 3", len(got.Inputs.Flags))
	}

	port := got.Inputs.Flags[0]
	if port.Schema.Minimum == nil || *port.Schema.Minimum != 1 {
		t.Errorf("port.minimum: got %v, want 1", port.Schema.Minimum)
	}
	if port.Schema.Maximum == nil || *port.Schema.Maximum != 65535 {
		t.Errorf("port.maximum: got %v, want 65535", port.Schema.Maximum)
	}
	if !port.Schema.RequiredBool() {
		t.Errorf("port.required: got false, want true")
	}

	name := got.Inputs.Flags[1]
	if name.Schema.MinLength == nil || *name.Schema.MinLength != 3 {
		t.Errorf("name.minLength: got %v, want 3", name.Schema.MinLength)
	}
	if name.Schema.MaxLength == nil || *name.Schema.MaxLength != 32 {
		t.Errorf("name.maxLength: got %v, want 32", name.Schema.MaxLength)
	}
	if name.Schema.Pattern == "" {
		t.Error("name.pattern: got empty, want non-empty")
	}
	if len(name.Schema.Enum) != 2 || name.Schema.Enum[0] != "alpha" {
		t.Errorf("name.enum: got %v, want [alpha beta]", name.Schema.Enum)
	}
	if len(name.Schema.Default) == 0 {
		t.Errorf("name.default: got empty, want non-empty")
	}

	tags := got.Inputs.Flags[2]
	if tags.Schema.MinItems == nil || *tags.Schema.MinItems != 1 {
		t.Errorf("tags.minItems: got %v, want 1", tags.Schema.MinItems)
	}
	if tags.Schema.MaxItems == nil || *tags.Schema.MaxItems != 10 {
		t.Errorf("tags.maxItems: got %v, want 10", tags.Schema.MaxItems)
	}
	if !tags.Schema.Nullable {
		t.Errorf("tags.nullable: got false, want true")
	}
}

// =============================================================================
// Top-level extras: schemas, configs, events, metadata, remote_commands
// =============================================================================

func TestLoadSpec_topLevelExtras(t *testing.T) {
	t.Parallel()
	content := `name: srv
schemas:
  Address:
    type: object
    properties:
      street:
        type: string
      zip:
        type: string
    required: ["street"]
files:
  - name: app
    path: ~/.config/srv.yaml
    format: yaml
events:
  - name: startup
remote_commands:
  - name: shell
    aliases: [sh]
    timeout: 30s
metadata:
  - var: Version
    default: "0.0.0"
`
	path := writeSpec(t, "spec.yaml", content)
	got, err := internal.LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if len(got.Schemas) != 1 {
		t.Errorf("Schemas: got %d, want 1", len(got.Schemas))
	}
	if addr, ok := got.Schemas["Address"]; !ok || addr == nil {
		t.Errorf("Schemas[Address]: missing")
	} else {
		fields := addr.RequiredFields()
		if len(fields) != 1 || fields[0] != "street" {
			t.Errorf("Address.RequiredFields: got %v, want [street]", fields)
		}
	}
	if len(got.Files) != 1 || got.Files[0].Format != "yaml" {
		t.Errorf("Files: got %+v, want one yaml config", got.Files)
	}
	if len(got.Events) != 1 || got.Events[0].Name != "startup" {
		t.Errorf("Events: got %+v, want one named startup", got.Events)
	}
	if len(got.RemoteCommands) != 1 || got.RemoteCommands[0].Name != "shell" {
		t.Errorf("RemoteCommands: got %+v, want one named shell", got.RemoteCommands)
	}
	if len(got.Metadata) != 1 || got.Metadata[0].Var != "Version" {
		t.Errorf("Metadata: got %+v, want one Var=Version", got.Metadata)
	}
}

// =============================================================================
// Conf loader
// =============================================================================

const minimalConfYAML = `generate:
  cmd:
    package: cmd
    gen_file: handlers.gen.go
    prune:
      enabled: true
      keep: ["custom.go"]
  framework:
    package: fw
    gen_file: rotini.gen.go
    additional_imports: ["fmt"]
`

func TestLoadConf_yaml(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "conf.yaml", minimalConfYAML)
	got, err := internal.LoadConf(path)
	if err != nil {
		t.Fatalf("LoadConf: %v", err)
	}
	if got.Generate.Cmd.Package != "cmd" {
		t.Errorf("cmd.package: got %q, want %q", got.Generate.Cmd.Package, "cmd")
	}
	if !got.Generate.Cmd.Prune.Enabled {
		t.Errorf("cmd.prune.enabled: got false, want true")
	}
	if len(got.Generate.Cmd.Prune.Keep) != 1 || got.Generate.Cmd.Prune.Keep[0] != "custom.go" {
		t.Errorf("cmd.prune.keep: got %v, want [custom.go]", got.Generate.Cmd.Prune.Keep)
	}
	if got.Generate.Framework.Package != "fw" {
		t.Errorf("framework.package: got %q, want %q", got.Generate.Framework.Package, "fw")
	}
	if len(got.Generate.Framework.AdditionalImports) != 1 {
		t.Errorf("framework.additional_imports: got %v, want [fmt]", got.Generate.Framework.AdditionalImports)
	}
}

func TestLoadConf_unknownField(t *testing.T) {
	t.Parallel()
	path := writeSpec(t, "conf.yaml", "generate:\n  bogus: 1\n")
	_, err := internal.LoadConf(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

// =============================================================================
// ApplyConfDefaults
// =============================================================================

func TestApplyConfDefaults_populatesEmpty(t *testing.T) {
	t.Parallel()
	var c internal.Conf
	internal.ApplyConfDefaults(&c)
	if c.Generate.Cmd.Package == "" {
		t.Errorf("cmd.package: still empty after ApplyConfDefaults")
	}
	if c.Generate.Cmd.GenFile == "" {
		t.Errorf("cmd.gen_file: still empty after ApplyConfDefaults")
	}
	if c.Generate.Framework.Package == "" {
		t.Errorf("framework.package: still empty after ApplyConfDefaults")
	}
	if c.Generate.Framework.GenFile == "" {
		t.Errorf("framework.gen_file: still empty after ApplyConfDefaults")
	}
}

func TestApplyConfDefaults_preservesNonEmpty(t *testing.T) {
	t.Parallel()
	c := &internal.Conf{
		Generate: internal.GenerateConfig{
			Cmd: internal.GenerateCmdConfig{Package: "mine"},
		},
	}
	internal.ApplyConfDefaults(c)
	if c.Generate.Cmd.Package != "mine" {
		t.Errorf("cmd.package: got %q, want %q (defaults overwrote non-empty)", c.Generate.Cmd.Package, "mine")
	}
}

func TestApplyConfDefaults_nilSafe(t *testing.T) {
	t.Parallel()
	internal.ApplyConfDefaults(nil) // must not panic
}
