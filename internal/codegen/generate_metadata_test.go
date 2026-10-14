package codegen

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const metadataSpec = `version: 0.0.0
command:
  name: app
  metadata: {tier: internal, owner: platform}
  config_files:
    - {name: main, path: ~/.app.yaml}
  flags:
    - name: force
      identifiers: [--force]
      metadata: {audit: "yes"}
      schema: {type: bool}
    - name: quiet
      identifiers: [--quiet]
      schema: {type: bool}
  arguments:
    - name: target
      metadata: {kind: host}
      schema: {type: string}
  env:
    - name: token
      metadata: {source: vault}
      schema: {type: string, variable: APP_TOKEN}
  config:
    - name: region
      metadata: {scope: global}
      schema: {type: string, key: region}
  commands:
    - name: rm
      metadata: {owner: storage}
      stdin: {format: text, metadata: {encoding: utf-8}}
`

// TestDefinitionLiteral_metadata pins that `metadata:` reaches the root, command, flag and
// argument literals verbatim, keys sorted, and that an input without it gets no field.
func TestDefinitionLiteral_metadata(t *testing.T) {
	gp, err := resolveTree(decodeSpecYAML(t, metadataSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	got := renderDefinition(gp)
	for _, want := range []string{
		`Metadata: map[string]string{"owner": "platform", "tier": "internal"},` + "\n",
		`Metadata: map[string]string{"owner": "storage"},` + "\n",
		`Type: "bool", Local: true, Metadata: map[string]string{"audit": "yes"}`,
		`, Metadata: map[string]string{"kind": "host"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definition lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Metadata:"); n != 4 {
		t.Errorf("Metadata appears %d times, want 4:\n%s", n, got)
	}
}

// TestContract_metadata pins that every command and input kind carries its `metadata:` into the
// contract, which still matches schema-contract.json, and that undeclared metadata is left out.
func TestContract_metadata(t *testing.T) {
	_, cmds := contractJSON(t, metadataSpec)
	root, rm := cmds["app"], cmds["app rm"]
	for name, c := range map[string]struct {
		got  any
		want map[string]any
	}{
		"root":   {root["metadata"], map[string]any{"owner": "platform", "tier": "internal"}},
		"rm":     {rm["metadata"], map[string]any{"owner": "storage"}},
		"flag":   {entry(t, root, "flags", "force")["metadata"], map[string]any{"audit": "yes"}},
		"arg":    {entry(t, root, "arguments", "target")["metadata"], map[string]any{"kind": "host"}},
		"env":    {entry(t, root, "env", "token")["metadata"], map[string]any{"source": "vault"}},
		"config": {entry(t, root, "config", "region")["metadata"], map[string]any{"scope": "global"}},
		"stdin":  {rm["stdin"].(map[string]any)["metadata"], map[string]any{"encoding": "utf-8"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s metadata = %v, want %v", name, c.got, c.want)
		}
	}
	if got, ok := entry(t, root, "flags", "quiet")["metadata"]; ok {
		t.Errorf("a flag without metadata has %v", got)
	}
}

// TestOverlayCommand_metadata pins that metadata set next to a `$ref` replaces the composed
// command's, and that the composed command keeps its own when the parent sets none.
func TestOverlayCommand_metadata(t *testing.T) {
	child := Command{Name: "db", Metadata: map[string]string{"owner": "data"}}
	if got := overlayCommand(child, Command{}).Metadata; !reflect.DeepEqual(got, child.Metadata) {
		t.Errorf("no overlay: metadata = %v, want the child's", got)
	}
	parent := Command{Metadata: map[string]string{"tier": "internal"}}
	if got := overlayCommand(child, parent).Metadata; !reflect.DeepEqual(got, parent.Metadata) {
		t.Errorf("overlay: metadata = %v, want the parent's", got)
	}
}

// TestValidate_metadataValues pins that metadata takes string values under non-empty keys.
func TestValidate_metadataValues(t *testing.T) {
	head := "version: 0.0.0\ncommand:\n  name: app\n  summary: s\n"
	for name, body := range map[string]string{
		"number value":     "  metadata: {replicas: 3}\n",
		"bool value":       "  metadata: {audit: true}\n",
		"nested value":     "  metadata: {owner: {team: a}}\n",
		"empty key":        "  metadata: {\"\": x}\n",
		"flag number":      "  flags:\n    - {name: f, identifiers: [--f], schema: {type: bool}, metadata: {n: 1}}\n",
		"stdin list value": "  stdin: {format: text, metadata: {tags: [a]}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err, _ := validateInModule(t, head+body, lintFixtureConf, ""); err == nil {
				t.Error("validate accepted it")
			}
		})
	}
	ok := head + "  metadata: {owner: platform, \"x.y/z\": \"1\"}\n"
	if err, warnings := validateInModule(t, ok, lintFixtureConf, ""); err != nil || len(warnings) > 0 {
		t.Errorf("validate rejected string metadata: %v %v", err, warnings)
	}
}
