package codegen

import (
	"reflect"
	"testing"
)

const expandFactsSpec = `version: 0.0.0
command:
  name: app
  config_files:
    - {name: user, discover: {strategy: xdg, app: app, file: config.yaml}}
  commands:
    - name: build
      arguments:
        - name: src
          schema: {type: existingdir, variable: APP_SRC, variable_file: APP_SRC_FILE, expand: [home, env]}
      flags:
        - name: cache
          identifiers: [--cache]
          schema: {type: string, key: cache, expand: [home], relative_to: config}
      env:
        - name: home
          schema: {type: existingdir, expand: [home]}
      config:
        - name: data
          schema: {type: "[]existingfile", expand: [home, env], relative_to: config}
`

// TestContract_expandFacts pins the contract's path-expansion facts on each kind of input.
func TestContract_expandFacts(t *testing.T) {
	_, cmds := contractJSON(t, expandFactsSpec)
	build := cmds["app build"]
	keys := []string{"expand", "relative_to", "variable_file"}
	for _, tt := range []struct {
		kind, name string
		want       map[string]any
	}{
		{"arguments", "src", map[string]any{"expand": []any{"home", "env"}, "variable_file": "APP_SRC_FILE"}},
		{"flags", "cache", map[string]any{"expand": []any{"home"}, "relative_to": "config"}},
		{"env", "home", map[string]any{"expand": []any{"home"}}},
		{"config", "data", map[string]any{"expand": []any{"home", "env"}, "relative_to": "config"}},
	} {
		if got := facts(entry(t, build, tt.kind, tt.name), keys...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %q facts = %v, want %v", tt.kind, tt.name, got, tt.want)
		}
	}
}

func TestExpansionAndPathTags(t *testing.T) {
	for _, tt := range []struct {
		schema       *InputSchema
		expand, path string
	}{
		{&InputSchema{Type: "existingfile", Expand: []string{"home", "env"}, RelativeTo: "config"}, `expand:"home,env" relativeto:"config"`, `path:"file"`},
		{&InputSchema{Type: "[]existingdir"}, "", `path:"dir"`},
		{&InputSchema{Type: "inputfile", Expand: []string{"home"}}, `expand:"home"`, ""},
		{nil, "", ""},
	} {
		if got := expansionTags(tt.schema); got != tt.expand {
			t.Errorf("expansionTags = %q, want %q", got, tt.expand)
		}
		if got := pathTag(tt.schema); got != tt.path {
			t.Errorf("pathTag = %q, want %q", got, tt.path)
		}
	}
	if got := joinTags("a", "", "b"); got != "a b" {
		t.Errorf("joinTags = %q", got)
	}
}

// Env and config fields carry the path and expansion tags; flags and arguments the expansion
// tags and, for an argument's environment fallback, the file variable.
func TestInputFields_expandTags(t *testing.T) {
	in := &Inputs{
		Flags:     []FlagInput{{Name: "out", Schema: &InputSchema{Expand: []string{"env"}}}},
		Arguments: []ArgumentInput{{Name: "src", Schema: &InputSchema{Variable: "APP_SRC", VariableFile: "APP_SRC_FILE", Expand: []string{"home"}}}},
		Env:       []EnvInput{{Name: "log", Schema: &InputSchema{Type: "existingfile", Expand: []string{"home"}}}},
		Config:    []ConfigInput{{Name: "data", Schema: &InputSchema{Type: "existingdir", RelativeTo: "config"}}},
	}
	if got := inputFieldTag(flagFields(in, "")[0]); got != "`rotini:\"out\" expand:\"env\"`" {
		t.Errorf("flag tag = %s", got)
	}
	if got := inputFieldTag(argFields(in, "")[0]); got != "`rotini:\"src\" recon:\"src\" env:\"APP_SRC\" envfile:\"APP_SRC_FILE\" expand:\"home\"`" {
		t.Errorf("argument tag = %s", got)
	}
	if got := inputFieldTag(envFields(in, "")[0]); got != "`rotini:\"log\" recon:\"log\" env:\"LOG\" expand:\"home\" path:\"file\"`" {
		t.Errorf("env tag = %s", got)
	}
	if got := inputFieldTag(configFields(in)[0]); got != "`rotini:\"data\" recon:\"data\" relativeto:\"config\" path:\"dir\"`" {
		t.Errorf("config tag = %s", got)
	}
}
