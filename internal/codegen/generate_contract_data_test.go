package codegen

import (
	"reflect"
	"testing"
)

const dataFactsSpec = `version: 0.0.0
command:
  name: app
  env_prefix: APP
  config_files:
    - {name: dotenv, path: .env, format: dotenv, as: env}
    - {name: system, discover: {strategy: xdg-system, app: app, file: config.yaml}}
    - {name: user, discover: {strategy: native, app: app, file: config.yaml}}
  commands:
    - name: cat
      arguments:
        - name: files
          schema: {type: "[]inputfile", glob: true}
      flags:
        - name: out
          identifiers: [-o, --out]
          schema: {type: outputfile}
        - name: force
          identifiers: [--force]
          role: force
          schema: {type: bool}
        - name: since
          identifiers: [--since]
          schema: {type: time, relative: past, layout: ["2006-01-02", unix]}
        - name: color
          identifiers: [--color]
          schema: {type: bool, negatable: --plain}
        - name: paths
          identifiers: [--paths]
          schema: {type: "[]string", separator: nul, from: [file]}
        - name: token
          identifiers: [--token]
          schema: {type: string, variable: APP_TOKEN, variable_file: APP_TOKEN_FILE}
      env:
        - name: search
          schema: {type: "[]string", variable: SEARCH_PATH, separator: ":"}
        - name: key
          schema: {type: string, variable: APP_KEY, variable_file: APP_KEY_FILE}
      config:
        - name: until
          schema: {type: date, relative: future}
      stdin:
        format: lines
        stream: true
        separator: nul
        unless_argument: files
`

// TestContract_dataFacts pins the facts about files, streams and value sources the contract
// states: what a caller needs to pipe data in, name files and set variables.
func TestContract_dataFacts(t *testing.T) {
	_, cmds := contractJSON(t, dataFactsSpec)
	root, cat := cmds["app"], cmds["app cat"]
	keys := []string{"type", "kind", "negated", "separator", "glob", "role", "layouts", "relative", "variable_file"}
	for _, tt := range []struct {
		kind, name string
		want       map[string]any
	}{
		{"arguments", "files", map[string]any{"type": "[]inputfile", "kind": "list", "glob": true}},
		{"flags", "out", map[string]any{"type": "outputfile", "kind": "scalar"}},
		{"flags", "force", map[string]any{"type": "bool", "kind": "scalar", "role": "force"}},
		{"flags", "since", map[string]any{"type": "time", "kind": "scalar", "layouts": []any{"2006-01-02", "unix"}, "relative": "past"}},
		{"flags", "color", map[string]any{"type": "bool", "kind": "scalar", "negated": []any{"--plain"}}},
		{"flags", "paths", map[string]any{"type": "[]string", "kind": "list", "separator": "nul"}},
		{"flags", "token", map[string]any{"type": "string", "kind": "scalar", "variable_file": "APP_TOKEN_FILE"}},
		{"env", "search", map[string]any{"type": "[]string", "kind": "list", "separator": ":"}},
		{"env", "key", map[string]any{"type": "string", "kind": "scalar", "variable_file": "APP_KEY_FILE"}},
		{"config", "until", map[string]any{"type": "date", "kind": "scalar", "layouts": []any{"2006-01-02"}, "relative": "future"}},
	} {
		if got := facts(entry(t, cat, tt.kind, tt.name), keys...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %q facts = %v, want %v", tt.kind, tt.name, got, tt.want)
		}
	}
	wantStdin := map[string]any{"format": "lines", "stream": true, "separator": "nul", "unless_argument": "files"}
	if got := facts(cat["stdin"].(map[string]any), "format", "stream", "separator", "unless_argument"); !reflect.DeepEqual(got, wantStdin) {
		t.Errorf("stdin = %v, want %v", got, wantStdin)
	}
	wantFiles := []any{
		map[string]any{"name": "dotenv", "path": ".env", "format": "dotenv", "as": "env"},
		map[string]any{"name": "system", "discover": map[string]any{"strategy": "xdg-system", "app": "app", "file": "config.yaml"}},
		map[string]any{"name": "user", "discover": map[string]any{"strategy": "native", "app": "app", "file": "config.yaml"}},
	}
	if got := root["config_files"]; !reflect.DeepEqual(got, wantFiles) {
		t.Errorf("config_files = %v, want %v", got, wantFiles)
	}
}
