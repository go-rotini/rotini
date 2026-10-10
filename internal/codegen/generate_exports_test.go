package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/contractdiff"
)

// exportsConf turns on the tool, skill and permission exports for the app fixtures below.
const exportsConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/app/zz_app.go
      package: app
  features:
    - type: tools
      enabled: true
    - type: skill
      enabled: true
      description: Use when the user asks to list things.
    - type: permissions
      enabled: true
`

// exportsSpec has a read command whose hidden flag is destructive, a plain read command, and a
// directory flag that opts into tool exports.
const exportsSpec = `version: 0.0.0
command:
  name: app
  flags:
    - name: dir
      summary: run as if started in this directory
      identifiers: [-C, --dir]
      cascading: true
      role: chdir
      agent: true
      schema: { type: existingdir }
  commands:
    - name: list
      summary: list things
      effects: { kind: read }
      flags:
        - name: wipe
          summary: delete everything first
          identifiers: [--wipe]
          hidden: true
          effects: { kind: destructive }
          schema: { type: bool }
    - name: show
      summary: show one thing
      effects: { kind: read }
`

// generateSpecModule generates spec under conf in a fresh module and returns its directory.
func generateSpecModule(t *testing.T, spec, conf string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	var genErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) { genErr = e }, func([]error) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if genErr != nil {
		t.Fatalf("Generate reported: %v", genErr)
	}
	return dir
}

// mcpToolsByName reads tools/mcp.json and returns its tools by name.
func mcpToolsByName(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, tl := range readJSON(t, filepath.Join(dir, "tools", "mcp.json")).(map[string]any)["tools"].([]any) {
		m := tl.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

// A flag a tool can't pass still counts toward the permission files and the skill's
// allowed-tools: a read command with a hidden destructive flag gets no allow rule, and asks
// when the flag is given. The MCP annotations describe what a tool call can do, so they stay
// read-only.
func TestExports_hiddenDestructiveFlag(t *testing.T) {
	dir := generateSpecModule(t, exportsSpec, exportsConf)

	claude := readJSON(t, filepath.Join(dir, "agents", "claude-settings.json")).(map[string]any)["permissions"].(map[string]any)
	allow, _ := claude["allow"].([]any)
	ask, _ := claude["ask"].([]any)
	if slices.Contains(allow, any("Bash(app list *)")) {
		t.Errorf("allow %v has app list, whose hidden flag deletes", allow)
	}
	if !slices.Contains(allow, any("Bash(app show *)")) {
		t.Errorf("allow %v lacks app show", allow)
	}
	if !slices.Contains(ask, any("Bash(app list *--wipe*)")) {
		t.Errorf("ask %v lacks app list with --wipe", ask)
	}

	codex, err := os.ReadFile(filepath.Join(dir, "agents", "app.rules"))
	if err != nil {
		t.Fatal(err)
	}
	checkCodexRules(t, string(codex))
	for block := range strings.SplitSeq(string(codex), "prefix_rule(") {
		if strings.Contains(block, `"list"`) && strings.Contains(block, `decision = "allow"`) {
			t.Errorf("Codex allows app list:\n%s", block)
		}
	}
	if !strings.Contains(string(codex), "(destructive with --wipe)") {
		t.Errorf("Codex doesn't prompt for app list:\n%s", codex)
	}
	policy, err := os.ReadFile(filepath.Join(dir, "agents", "app-policy.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for block := range strings.SplitSeq(string(policy), "[[rule]]") {
		if strings.Contains(block, "list") && strings.Contains(block, `decision = "allow"`) {
			t.Errorf("Gemini allows app list:\n%s", block)
		}
	}
	if !strings.Contains(string(policy), "--wipe") {
		t.Errorf("Gemini doesn't ask for app list --wipe:\n%s", policy)
	}

	page, err := os.ReadFile(filepath.Join(dir, "skills", "app", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "\nallowed-tools: \"Bash(app show *)\"\n") {
		t.Errorf("allowed-tools should name app show only:\n%s", page)
	}

	ann := mcpToolsByName(t, dir)["app_list"]["annotations"].(map[string]any)
	if ann["readOnlyHint"] != true {
		t.Errorf("app_list annotations %v, want read-only: a tool can't pass --wipe", ann)
	}
}

// The directory flag is a tool parameter only when it says `agent: true`.
func TestExports_chdirFlag(t *testing.T) {
	params := func(tool map[string]any) map[string]any {
		return tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	}
	tools := mcpToolsByName(t, generateSpecModule(t, exportsSpec, exportsConf))
	for _, name := range []string{"app_list", "app_show"} {
		if _, ok := params(tools[name])["dir"]; !ok {
			t.Errorf("%s lacks the opted-in directory flag: %v", name, params(tools[name]))
		}
	}
	inv := tools["app_show"]["_meta"].(map[string]any)[toolsMetaKey].(map[string]any)
	if fl := inv["flags"].(map[string]any)["dir"].(map[string]any); fl["flag"] != "--dir" || fl["role"] != "chdir" {
		t.Errorf("dir invoke facts %v", fl)
	}

	tools = mcpToolsByName(t, generateSpecModule(t, strings.Replace(exportsSpec, "      agent: true\n", "", 1), exportsConf))
	if _, ok := params(tools["app_show"])["dir"]; ok {
		t.Error("the directory flag is a tool parameter without `agent: true`")
	}
}

// Deprecated enum values stay accepted, so the input's schema lists them, but the contract's
// parameters and the tool definitions leave them out.
func TestExports_deprecatedEnumValues(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: app
  commands:
    - name: run
      effects: { kind: read }
      arguments:
        - name: speed
          schema:
            type: string
            enum: [fast, { value: slow, deprecated: use fast }]
      flags:
        - name: modes
          identifiers: [--modes]
          schema:
            type: '[]string'
            enum: [fast, { value: slow, deprecated: use fast }]
`
	enumOf := func(s map[string]any) []any {
		if items, ok := s["items"].(map[string]any); ok {
			s = items
		}
		e, _ := s["enum"].([]any)
		return e
	}
	want := []any{"fast"}

	_, cmds := contractJSON(t, spec)
	run := cmds["app run"]
	if got := enumOf(entry(t, run, "flags", "modes")["schema"].(map[string]any)); len(got) != 2 {
		t.Errorf("the flag's schema enum %v should keep the deprecated value", got)
	}
	props := run["parameters"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"speed", "modes"} {
		if got := enumOf(props[name].(map[string]any)); !slices.Equal(got, want) {
			t.Errorf("parameters %s enum %v, want %v", name, got, want)
		}
	}

	dir := generateSpecModule(t, spec, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/app/zz_app.go
      package: app
  features:
    - type: tools
      enabled: true
      targets: [mcp, openai-strict, gemini]
`)
	tool := mcpToolsByName(t, dir)["app_run"]
	props = tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"speed", "modes"} {
		if got := enumOf(props[name].(map[string]any)); !slices.Equal(got, want) {
			t.Errorf("mcp.json %s enum %v, want %v", name, got, want)
		}
	}
	for _, file := range []string{"openai.json", "gemini.json"} {
		b, err := os.ReadFile(filepath.Join(dir, "tools", file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"slow"`) {
			t.Errorf("%s lists the deprecated value:\n%s", file, b)
		}
	}
}

// An argument's `from` is in the contract, and a flag's env list holds variable names only: the
// file variable is its own field.
func TestContract_argumentFromAndFlagEnv(t *testing.T) {
	_, cmds := contractJSON(t, `version: 0.0.0
command:
  name: app
  commands:
    - name: read
      arguments:
        - name: src
          schema: { type: string, from: [file, stdin] }
      flags:
        - name: token
          identifiers: [--token]
          schema: { type: string, variable: APP_TOKEN, variable_file: APP_TOKEN_FILE }
`)
	run := cmds["app read"]
	if got := entry(t, run, "arguments", "src")["from"]; !slices.Equal(toStrings(got), []string{"file", "stdin"}) {
		t.Errorf("argument from = %v", got)
	}
	tok := entry(t, run, "flags", "token")
	if got := toStrings(tok["env"]); !slices.Equal(got, []string{"APP_TOKEN"}) {
		t.Errorf("flag env = %q, want the variable names only", got)
	}
	if tok["variable_file"] != "APP_TOKEN_FILE" {
		t.Errorf("flag variable_file = %v", tok["variable_file"])
	}
}

// toStrings converts a decoded JSON array of strings.
func toStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, s := range list {
		str, _ := s.(string)
		out = append(out, str)
	}
	return out
}

// A command that reads stdin can't also take a tool parameter named stdin: generate fails, as
// validate does, rather than leaving the tool out.
func TestToolExports_stdinClash(t *testing.T) {
	a := &agentProgram{doc: agentDoc{Name: "app"}}
	a.commands = []agentCommand{{
		c: agentContractCmd{
			Path:      []string{"load"},
			Arguments: []agentInput{{Name: "stdin", Kind: "scalar", Schema: map[string]any{"type": "string"}}},
			Stdin:     &contractStdin{Format: "text"},
		},
		leaf: true, offered: true, invocation: "app load",
	}}
	_, notices, err := buildTools(a, false)
	if err == nil || !strings.Contains(err.Error(), `tools: "app load" reads stdin and also has an input named "stdin"`) {
		t.Errorf("err = %v, notices = %v; want the clash as an error", err, notices)
	}
}

// ToolsMCP is named in a generated-name collision.
func TestGeneratedNames_toolsMCP(t *testing.T) {
	if _, ok := generatedNames(nil, false, false)["ToolsMCP"]; ok {
		t.Error("ToolsMCP is named without the tools feature's go: true")
	}
	err := duplicateDecls("x.go", generatedNames(nil, true, true), []byte("package p\n\nvar ToolsMCP = 1\n\ntype ToolsMCP struct{}\n"))
	if err == nil || !strings.Contains(err.Error(), "the tools feature's ToolsMCP variable (go: true) makes the same Go name") {
		t.Errorf("duplicateDecls = %v", err)
	}
}

// The contract diff sees an argument's `from` change, now that the contract carries it.
func TestContract_argumentFromDiff(t *testing.T) {
	spec := func(from string) string {
		return `version: 0.0.0
command:
  name: app
  commands:
    - name: read
      arguments:
        - name: src
          schema: { type: string` + from + ` }
`
	}
	rules := func(before, after string) []string {
		a, _ := contractJSON(t, spec(before))
		b, _ := contractJSON(t, spec(after))
		r, err := contractdiff.Diff(a, b, contractdiff.Options{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range r.Findings {
			out = append(out, string(f.Severity)+" "+f.Rule+" "+f.Where)
		}
		return out
	}
	if got := rules(", from: [file]", ", from: [file, stdin]"); !slices.Equal(got, []string{"possibly_breaking INPUT_FROM_ADDED app read <src>"}) {
		t.Errorf("stdin added: %q", got)
	}
	if got := rules(", from: [file]", ""); !slices.Equal(got, []string{"breaking INPUT_FROM_NO_DELETE app read <src>"}) {
		t.Errorf("file removed: %q", got)
	}
}
