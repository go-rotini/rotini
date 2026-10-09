package codegen

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// contractJSON builds the contract for spec in memory, checks it against schema-contract.json,
// and returns it decoded, with its commands by name.
func contractJSON(t *testing.T, spec string) (raw []byte, commands map[string]map[string]any) {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Fatalf("the contract does not match schema-contract.json: %v\n%s", problems, raw)
	}
	var doc struct {
		Commands []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	commands = map[string]map[string]any{}
	for _, c := range doc.Commands {
		commands[c["name"].(string)] = c
	}
	return raw, commands
}

// entry returns the named input of kind (arguments, flags, env, config) on a command.
func entry(t *testing.T, cmd map[string]any, kind, name string) map[string]any {
	t.Helper()
	list, _ := cmd[kind].([]any)
	for _, e := range list {
		if m, _ := e.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("%s has no %s %q: %v", cmd["name"], kind, name, cmd[kind])
	return nil
}

// facts keeps only the listed keys of an entry, for comparing the facts a test is about.
func facts(e map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := e[k]; ok {
			out[k] = v
		}
	}
	return out
}

func asJSON(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

const factsSpec = `version: 0.0.0
command:
  name: app
  env_prefix: APP
  schemas:
    DB:
      type: object
      properties:
        host: {type: string}
  config_files:
    - {name: main, path: ~/.app.yaml}
    - {name: project, format: toml, discover: {strategy: walk-up, file: .app.toml}}
  plugin_discovery: {hidden: true}
  flags:
    - name: color
      identifiers: [--color, -c]
      schema: {type: bool, negatable: true}
    - name: no-cache
      identifiers: [--no-cache]
      schema: {type: bool}
    - name: cache
      identifiers: [--cache]
      schema: {type: bool, negatable: true}
  commands:
    - name: deploy
      aliases: [ship, push]
      deprecated_identifiers: [push]
      flags:
        - name: small
          identifiers: [--small]
          schema: {type: int8}
        - name: plain
          identifiers: [--plain]
          schema: {type: integer}
        - name: verbose
          identifiers: [-v, --verbose]
          schema: {type: count}
        - name: tags
          identifiers: [--tags]
          schema: {type: array, items: {type: string}, separator: ","}
        - name: labels
          identifiers: [--label]
          schema: {type: "map[string]string"}
        - name: set
          identifiers: [--set]
          schema: {type: map, dotted_keys: true}
        - name: db
          identifiers: [--db]
          schema: {$ref: "#/schemas/DB"}
        - name: token
          identifiers: [--token]
          schema: {type: string, from: [file, stdin], secret: true}
        - name: mode
          identifiers: [--mode]
          schema: {type: string, enum: [auto, always, never], implicit_value: always, ignore_case: true}
        - name: day
          identifiers: [--day]
          schema: {type: date}
        - name: conf
          identifiers: [--conf, --cfg]
          deprecated_identifiers: [--cfg]
          schema: {type: string, config_source: project}
        - name: a
          identifiers: [--a]
          schema: {type: bool}
        - name: b
          identifiers: [--b]
          schema: {type: bool}
      flag_groups:
        - {kind: mutually_exclusive, flags: [a, b]}
      flag_dependencies:
        - {when: a, requires: [small]}
      arguments:
        - name: hosts
          schema: {type: "[]string", separator: ";", secret: true}
      env:
        - name: http
          schema: {type: map, variable: APP_HTTP, nesting: "__"}
        - name: region
          schema: {type: string, enum: [eu, us], ignore_case: true}
      config:
        - name: pass
          schema: {type: string, key: auth.pass, secret: true}
    - name: exec
      passthrough: true
      arguments:
        - name: argv
          schema: {type: "[]string"}
`

// TestContract_parsingFacts pins each fact the contract states beside an input's schema, and
// the command-level ones, as a caller needs them to build a command line.
func TestContract_parsingFacts(t *testing.T) {
	_, cmds := contractJSON(t, factsSpec)
	root, deploy := cmds["app"], cmds["app deploy"]
	keys := []string{
		"type", "kind", "negated", "separator", "from", "implicit_value", "ignore_case", "layouts",
		"secret", "deprecated_identifiers", "config_source", "dotted_keys", "nesting",
	}
	for _, tt := range []struct {
		cmd  map[string]any
		kind string
		name string
		want map[string]any
	}{
		// A negated form for each long identifier, none for the short one; a declared
		// identifier keeps its own name.
		{root, "flags", "color", map[string]any{"type": "bool", "kind": "scalar", "negated": []any{"--no-color"}}},
		{root, "flags", "cache", map[string]any{"type": "bool", "kind": "scalar"}},
		// JSON Schema's integer can't tell int8 from int; the type can.
		{deploy, "flags", "small", map[string]any{"type": "int8", "kind": "scalar"}},
		{deploy, "flags", "plain", map[string]any{"type": "int", "kind": "scalar"}},
		{deploy, "flags", "verbose", map[string]any{"type": "count", "kind": "count"}},
		{deploy, "flags", "tags", map[string]any{"type": "[]string", "kind": "list", "separator": ","}},
		{deploy, "flags", "labels", map[string]any{"type": "map[string]string", "kind": "map"}},
		{deploy, "flags", "set", map[string]any{"type": "map[string]any", "kind": "map", "dotted_keys": true}},
		{deploy, "flags", "db", map[string]any{"kind": "object"}},
		{deploy, "flags", "token", map[string]any{"type": "string", "kind": "scalar", "from": []any{"file", "stdin"}, "secret": true}},
		{deploy, "flags", "mode", map[string]any{"type": "string", "kind": "scalar", "implicit_value": "always", "ignore_case": true}},
		{deploy, "flags", "day", map[string]any{"type": "date", "kind": "scalar", "layouts": []any{"2006-01-02"}}},
		{deploy, "flags", "conf", map[string]any{"type": "string", "kind": "scalar", "config_source": "project", "deprecated_identifiers": []any{"--cfg"}}},
		{deploy, "arguments", "hosts", map[string]any{"type": "[]string", "kind": "list", "separator": ";", "secret": true}},
		{deploy, "env", "http", map[string]any{"type": "map[string]any", "kind": "map", "nesting": "__"}},
		{deploy, "env", "region", map[string]any{"type": "string", "kind": "scalar", "ignore_case": true}},
		{deploy, "config", "pass", map[string]any{"type": "string", "kind": "scalar", "secret": true}},
	} {
		if got := facts(entry(t, tt.cmd, tt.kind, tt.name), keys...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %s %q facts = %v, want %v", tt.cmd["name"], tt.kind, tt.name, got, tt.want)
		}
	}

	for _, tt := range []struct {
		cmd  map[string]any
		key  string
		want any
	}{
		{root, "config_files", []any{
			map[string]any{"name": "main", "path": "~/.app.yaml"},
			map[string]any{"name": "project", "format": "toml", "discover": map[string]any{"strategy": "walk-up", "file": ".app.toml"}},
		}},
		{root, "plugin_discovery", map[string]any{"prefix": "app-", "hidden": true}},
		{deploy, "deprecated_identifiers", []any{"push"}},
		{deploy, "flag_groups", []any{map[string]any{"kind": "mutually_exclusive", "flags": []any{"a", "b"}}}},
		{deploy, "flag_dependencies", []any{map[string]any{"when": "a", "requires": []any{"small"}}}},
		{cmds["app exec"], "passthrough", true},
	} {
		if got := tt.cmd[tt.key]; !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %s = %v, want %v", tt.cmd["name"], tt.key, got, tt.want)
		}
	}
}

// TestContract_parametersStayStandard pins that the rotini-only facts sit beside the schemas,
// never inside them, so `parameters` stays a plain JSON Schema a tool definition can use.
func TestContract_parametersStayStandard(t *testing.T) {
	_, cmds := contractJSON(t, factsSpec)
	params, _ := json.Marshal(cmds["app deploy"]["parameters"])
	for _, key := range []string{`"kind"`, `"negated"`, `"layouts"`, `"separator"`, `"secret"`, `"x-`, `"from"`, `"import"`} {
		if strings.Contains(string(params), key) {
			t.Errorf("parameters holds %s:\n%s", key, params)
		}
	}
}

const hiddenSpec = `version: 0.0.0
command:
  name: app
  flags:
    - {name: trace, identifiers: [--trace], cascading: true, hidden: true, schema: {type: bool}}
  commands:
    - name: run
      arguments:
        - {name: target, schema: {type: string}}
        - {name: extra, hidden: true, schema: {type: string}}
      flags:
        - {name: fast, identifiers: [--fast], schema: {type: bool}}
        - {name: debug, identifiers: [--debug], hidden: true, schema: {type: bool}}
      env:
        - {name: token, hidden: true, schema: {type: string}}
      config:
        - {name: level, hidden: true, schema: {type: string}}
    - name: internal
      hidden: true
      output: {type: object, properties: {ok: {type: boolean}}}
      commands:
        - name: dump
          summary: dump state
`

// TestContract_hiddenItemsListed pins that hidden commands and inputs are listed with
// hidden: true, so a hidden item can be told from a removed one, and that parameters, which
// says what a caller may pass, leaves them out.
func TestContract_hiddenItemsListed(t *testing.T) {
	_, cmds := contractJSON(t, hiddenSpec)
	run := cmds["app run"]
	for _, e := range []struct{ kind, name string }{
		{"arguments", "extra"}, {"flags", "debug"}, {"flags", "trace"}, {"env", "token"}, {"config", "level"},
	} {
		if entry(t, run, e.kind, e.name)["hidden"] != true {
			t.Errorf("%s %q is not marked hidden", e.kind, e.name)
		}
	}
	if entry(t, run, "flags", "fast")["hidden"] != nil {
		t.Error("a visible flag is marked hidden")
	}
	props := run["parameters"].(map[string]any)["properties"].(map[string]any)
	if got := slices.Sorted(maps.Keys(props)); !slices.Equal(got, []string{"fast", "target"}) {
		t.Errorf("parameters properties = %v, want the visible inputs only", got)
	}
	for _, name := range []string{"app internal", "app internal dump"} {
		if cmds[name]["hidden"] != true {
			t.Errorf("%s is not marked hidden: a hidden command's subtree is hidden too", name)
		}
	}
	if cmds["app run"]["hidden"] != nil {
		t.Error("a visible command is marked hidden")
	}
}

// TestOutputSchemaFiles_skipHidden pins that a hidden command gets no output schema file,
// though the contract lists it.
func TestOutputSchemaFiles_skipHidden(t *testing.T) {
	dir, _ := emitModule(t, hiddenSpec, contractConf)
	entries, err := os.ReadDir(filepath.Join(dir, "schemas", "output"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("output schema files = %v, want none for the hidden command", entries)
	}
}

const standaloneSpec = `version: 0.0.0
command:
  name: app
  schemas:
    Node:
      type: object
      properties:
        name: {type: string}
        children: {type: array, items: {$ref: "#/schemas/Node"}}
    Problem:
      type: object
      required: [reason]
      properties:
        reason: {type: string}
  commands:
    - name: tree
      flags:
        - {name: root, identifiers: [--root], schema: {$ref: "#/schemas/Node"}}
      stdin:
        format: json
        schema: {$ref: "#/schemas/Node", required: true}
      output: {$ref: "#/schemas/Node"}
      exit_status:
        - {code: 3, summary: partial, output: {type: array, items: {$ref: "#/schemas/Problem"}}}
`

// TestContract_selfContainedSchemas pins that each command's parameters, output, exit output
// and stdin schema compile and validate on their own, carrying the definitions they reach,
// recursive ones included.
func TestContract_selfContainedSchemas(t *testing.T) {
	_, cmds := contractJSON(t, standaloneSpec)
	tree := cmds["app tree"]
	exit := tree["exit_status"].([]any)[0].(map[string]any)
	for _, tt := range []struct {
		name   string
		schema any
		good   string
		bad    string
	}{
		{"parameters", tree["parameters"], `{"root":{"name":"a","children":[{"name":"b"}]}}`, `{"root":{"name":1}}`},
		{"output", tree["output"], `{"name":"a","children":[]}`, `{"children":[{"name":2}]}`},
		{"exit output", exit["output"], `[{"reason":"x"}]`, `[{}]`},
		{"stdin", tree["stdin"].(map[string]any)["schema"], `{"name":"a"}`, `{"name":false}`},
	} {
		raw, _ := json.Marshal(tt.schema)
		m := tt.schema.(map[string]any)
		if m["$schema"] != draft07 {
			t.Errorf("%s does not declare draft-07: %s", tt.name, raw)
		}
		compiled, err := compileSchema(tt.name, raw)
		if err != nil {
			t.Fatalf("%s does not compile on its own: %v\n%s", tt.name, err, raw)
		}
		if problems := validateInstance(tt.name, []byte(tt.good), compiled); len(problems) > 0 {
			t.Errorf("%s rejects %s: %v", tt.name, tt.good, problems)
		}
		if problems := validateInstance(tt.name, []byte(tt.bad), compiled); len(problems) == 0 {
			t.Errorf("%s accepts %s", tt.name, tt.bad)
		}
	}
	// A per-input schema still refers to the document's definitions.
	if ref := entry(t, tree, "flags", "root")["schema"].(map[string]any)["$ref"]; ref != "#/definitions/Node" {
		t.Errorf("the flag's schema = %v, want a reference into the document's definitions", ref)
	}
}

// TestContract_timeFormatsFollowLayout pins that a time input claims JSON Schema's date-time
// or date format only when it is read with that layout.
func TestContract_timeFormatsFollowLayout(t *testing.T) {
	_, cmds := contractJSON(t, `version: 0.0.0
command:
  name: app
  flags:
    - {name: t, identifiers: [--t], schema: {type: time}}
    - {name: rfc, identifiers: [--rfc], schema: {type: time, layout: "2006-01-02T15:04:05Z07:00"}}
    - {name: unix, identifiers: [--unix], schema: {type: time, layout: unix}}
    - {name: milli, identifiers: [--milli], schema: {type: datetime, layout: unixmilli}}
    - {name: day, identifiers: [--day], schema: {type: time, layout: "2006-01-02"}}
    - {name: date, identifiers: [--date], schema: {type: date}}
    - {name: eu, identifiers: [--eu], schema: {type: date, layout: "02/01/2006"}}
    - {name: stamps, identifiers: [--stamps], schema: {type: "[]time", layout: unix}}
    - {name: dates, identifiers: [--dates], schema: {type: array, items: {type: date}}}
    - {name: when, identifiers: [--when], schema: {type: "map[string]time"}}
`)
	root := cmds["app"]
	params := root["parameters"].(map[string]any)["properties"].(map[string]any)
	for name, want := range map[string]map[string]any{
		"t":      {"type": "string", "format": "date-time"},
		"rfc":    {"type": "string", "format": "date-time"},
		"unix":   {"type": "string"},
		"milli":  {"type": "string"},
		"day":    {"type": "string", "format": "date"},
		"date":   {"type": "string", "format": "date"},
		"eu":     {"type": "string"},
		"stamps": {"type": "array", "items": map[string]any{"type": "string"}},
		"dates":  {"type": "array", "items": map[string]any{"type": "string", "format": "date"}},
		"when":   {"type": "object", "additionalProperties": map[string]any{"type": "string", "format": "date-time"}},
	} {
		if got := entry(t, root, "flags", name)["schema"]; !reflect.DeepEqual(got, asJSON(want)) {
			t.Errorf("--%s schema = %v, want %v", name, got, want)
		}
		param, _ := params[name].(map[string]any)
		param = maps.Clone(param)
		delete(param, "description")
		if !reflect.DeepEqual(param, asJSON(want)) {
			t.Errorf("--%s parameter = %v, want %v", name, param, want)
		}
	}
	if got := entry(t, root, "flags", "eu")["layouts"]; !reflect.DeepEqual(got, []any{"02/01/2006"}) {
		t.Errorf("--eu layouts = %v", got)
	}
}

// TestContract_composedSchemas pins that a composed spec's named schemas reach the document's
// definitions, so its commands' references resolve, and that a name two specs use for
// different schemas is kept apart.
func TestContract_composedSchemas(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "child/.rotini.spec.yaml", `version: 0.0.0
command:
  name: child
  schemas:
    Item:
      type: object
      properties:
        sku: {type: string}
    Shared:
      type: object
      properties:
        id: {type: integer}
  commands:
    - name: show
      output: {$ref: "#/schemas/Item"}
      exit_status:
        - {code: 3, output: {$ref: "#/schemas/Shared"}}
`)
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: app
  schemas:
    Item:
      type: object
      properties:
        name: {type: string}
    Shared:
      type: object
      properties:
        id: {type: integer}
  output: {$ref: "#/schemas/Item"}
  commands:
    - $ref: ./child/.rotini.spec.yaml
`)
	gp, err := resolveTree(decodeSpecYAML(t, readTestFile(t, filepath.Join(dir, ".rotini.spec.yaml"))), filepath.Join(dir, ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Definitions map[string]any    `json:"definitions"`
		Commands    []contractCommand `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(doc.Definitions)); !slices.Equal(got, []string{"Item", "Shared", "child.Item"}) {
		t.Errorf("definitions = %v, want the root's, plus the child's differing Item renamed", got)
	}
	for _, c := range doc.Commands {
		if c.Name != "app child show" {
			continue
		}
		out, _ := json.Marshal(c.Output)
		if !strings.Contains(string(out), `"$ref":"#/definitions/child.Item"`) || !strings.Contains(string(out), `"sku"`) {
			t.Errorf("the composed command's output = %s, want the child's Item", out)
		}
		exitOut, _ := json.Marshal(c.ExitStatus[0].Output)
		if !strings.Contains(string(exitOut), `"$ref":"#/definitions/Shared"`) {
			t.Errorf("an identical schema keeps its name: %s", exitOut)
		}
		return
	}
	t.Fatal("app child show is missing")
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestContract_prototypeMisses pins the changes a contract diff could not see before: each is
// now a visible fact.
func TestContract_prototypeMisses(t *testing.T) {
	_, cmds := contractJSON(t, factsSpec+`    - name: quiet
      hidden: true
`)
	deploy := cmds["app deploy"]
	t.Run("negatable", func(t *testing.T) {
		if got := entry(t, cmds["app"], "flags", "color")["negated"]; !reflect.DeepEqual(got, []any{"--no-color"}) {
			t.Errorf("negated = %v", got)
		}
	})
	t.Run("int to int8", func(t *testing.T) {
		if entry(t, deploy, "flags", "small")["type"] != "int8" || entry(t, deploy, "flags", "plain")["type"] != "int" {
			t.Error("the rotini type does not tell int8 from int")
		}
	})
	t.Run("deprecated alias", func(t *testing.T) {
		if !reflect.DeepEqual(deploy["deprecated_identifiers"], []any{"push"}) {
			t.Errorf("deprecated_identifiers = %v", deploy["deprecated_identifiers"])
		}
	})
	t.Run("moved config file", func(t *testing.T) {
		files, _ := json.Marshal(cmds["app"]["config_files"])
		if !strings.Contains(string(files), `"path":"~/.app.yaml"`) {
			t.Errorf("config_files = %s", files)
		}
	})
	t.Run("hidden, not removed", func(t *testing.T) {
		if cmds["app quiet"]["hidden"] != true {
			t.Error("a hidden command is not listed as hidden")
		}
	})
}
