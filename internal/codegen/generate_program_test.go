package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// programConf turns on every whole-program feature, plus markdown so the skill has reference
// pages.
const programConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/taskr/zz_taskr.go
      package: taskr
  features:
    - type: markdown
      enabled: true
    - type: tools
      enabled: true
      targets: [mcp, openai-strict, gemini]
      go: true
    - type: skill
      enabled: true
      description: Use when the user asks to add, list or clean up tasks.
    - type: llms
      enabled: true
      base_url: https://taskr.example.com/cli/
    - type: permissions
      enabled: true
`

// programOutputs are the files the whole-program features write, relative to the module.
var programOutputs = []string{
	"tools/mcp.json", "tools/openai.json", "tools/gemini.json",
	"skills/taskr/SKILL.md", "llms.txt",
	"agents/claude-settings.json", "agents/taskr.rules", "agents/taskr-policy.toml",
}

// generateProgram generates the program fixture in a fresh module and returns its directory
// and the generate notices.
func generateProgram(t *testing.T, conf string) (string, []error) {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join(programDir, "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/taskr\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", string(spec))
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	var notices []error
	var genErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) { genErr = e }, func(n []error) { notices = n }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if genErr != nil {
		t.Fatalf("Generate reported: %v", genErr)
	}
	return dir, notices
}

// TestProgramFeatures_golden pins every whole-program feature's files. Run with -update to
// refresh them.
func TestProgramFeatures_golden(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("testdata", "program", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	dir, notices := generateProgram(t, programConf)
	files := append(slices.Clone(programOutputs), "skills/taskr/references/taskr-add.md")
	for _, rel := range files {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("%s wasn't written: %v", rel, err)
		}
		path := filepath.Join(golden, rel)
		if *updateGolden {
			writeTestFile(t, golden, rel, string(got))
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", rel, got, want)
		}
	}
	var msgs []string
	for _, n := range notices {
		msgs = append(msgs, n.Error())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{
		`"taskr list" is left out of openai.json: it has a map or free-form object parameter`,
		`"taskr remote login" is left out of the tool definitions: its required flag "keyfile" isn't a tool parameter`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("notices %q lack %q", joined, want)
		}
	}
	cmd, err := os.ReadFile(filepath.Join(dir, "internal", "cmd", "taskr", "zz_taskr.go"))
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := os.ReadFile(filepath.Join(dir, "tools", "mcp.json"))
	if !strings.Contains(string(cmd), "var ToolsMCP = `"+string(mcp)+"`") {
		t.Error("the cmd file's ToolsMCP isn't tools/mcp.json")
	}
}

// readJSON decodes a generated JSON file.
func readJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// TestToolExports_validate checks tools/mcp.json against both vendored MCP schemas (each tool
// against Tool, each schema compiled as JSON Schema 2020-12), the OpenAI strict-mode rules, and
// Gemini's parameter names.
func TestToolExports_validate(t *testing.T) {
	dir, _ := generateProgram(t, programConf)
	// mcp.json is written for 2026-07-28 unless mcp_revision asks for the older revision.
	older, _ := generateProgram(t, strings.Replace(programConf, "      go: true\n", "      go: true\n      mcp_revision: 2025-11-25\n", 1))
	for rev, at := range map[string]string{"2026-07-28": dir, "2025-11-25": older} {
		tools := readJSON(t, filepath.Join(at, "tools", "mcp.json")).(map[string]any)["tools"].([]any)
		if len(tools) == 0 {
			t.Fatal("no tools")
		}
		t.Run("mcp "+rev, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(mcpSchemaDir, "schema-"+rev+".json"))
			if err != nil {
				t.Fatal(err)
			}
			c := jsonschema.NewCompiler()
			if err := c.AddResource("https://modelcontextprotocol.io/schema.json", src); err != nil {
				t.Fatal(err)
			}
			toolSchema, err := c.Compile([]byte(`{"$ref":"https://modelcontextprotocol.io/schema.json#/$defs/Tool"}`))
			if err != nil {
				t.Fatal(err)
			}
			for _, tl := range tools {
				m := tl.(map[string]any)
				res, err := toolSchema.ValidateValue(tl)
				if err != nil {
					t.Fatal(err)
				}
				if !res.Valid {
					t.Errorf("%v isn't a valid MCP %s Tool: %v", m["name"], rev, res.Errors)
				}
				for _, k := range []string{"inputSchema", "outputSchema"} {
					if s, ok := m[k]; ok {
						if _, err := jsonschema.CompileValue(s, jsonschema.WithDefaultDraft(jsonschema.Draft202012)); err != nil {
							t.Errorf("%v %s doesn't compile as 2020-12: %v", m["name"], k, err)
						}
						b, _ := json.Marshal(s)
						if strings.Contains(string(b), `"definitions"`) || strings.Contains(string(b), "#/definitions/") {
							t.Errorf("%v %s still uses draft-07 definitions", m["name"], k)
						}
					}
				}
			}
		})
	}
	t.Run("openai-strict", func(t *testing.T) {
		list := readJSON(t, filepath.Join(dir, "tools", "openai.json")).([]any)
		for _, tl := range list {
			m := tl.(map[string]any)
			if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(m["name"].(string)) || m["strict"] != true || m["type"] != "function" {
				t.Errorf("bad tool header %v", m)
			}
			checkStrict(t, m["name"].(string), m["parameters"], 0)
		}
	})
	t.Run("gemini", func(t *testing.T) {
		doc := readJSON(t, filepath.Join(dir, "tools", "gemini.json")).(map[string]any)
		for _, d := range doc["functionDeclarations"].([]any) {
			m := d.(map[string]any)
			props := m["parametersJsonSchema"].(map[string]any)["properties"].(map[string]any)
			for name := range props {
				if !geminiName.MatchString(name) {
					t.Errorf("%v: parameter %q breaks Gemini's name rule", m["name"], name)
				}
			}
			b, _ := json.Marshal(m)
			if strings.Contains(string(b), `"$ref"`) {
				t.Errorf("%v keeps a $ref", m["name"])
			}
		}
	})
}

// programDir holds the program fixture, its goldens and the vendored MCP schemas, as an
// absolute path, since generateProgram changes directory.
var programDir = func() string {
	d, _ := filepath.Abs(filepath.Join("testdata", "program"))
	return d
}()

// mcpSchemaDir holds the vendored MCP schemas.
var mcpSchemaDir = filepath.Join(programDir, "mcpschema")

// checkStrict asserts OpenAI's strict-mode rules over a schema: every object closed, every
// property required, at most 10 levels.
func checkStrict(t *testing.T, tool string, v any, depth int) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if depth > 10 {
		t.Errorf("%s nests deeper than 10", tool)
	}
	if typeIs(m, "object") {
		if m["additionalProperties"] != false {
			t.Errorf("%s: an object isn't closed: %v", tool, m)
		}
		props, _ := m["properties"].(map[string]any)
		req, _ := m["required"].([]any)
		if len(req) != len(props) {
			t.Errorf("%s: not every property is required: %v", tool, m)
		}
		for _, p := range props {
			checkStrict(t, tool, p, depth+1)
		}
	}
	for _, k := range []string{"minLength", "maxLength", "uniqueItems", "default", "allOf", "not"} {
		if _, has := m[k]; has {
			t.Errorf("%s: strict mode doesn't take %q", tool, k)
		}
	}
	checkStrict(t, tool, m["items"], depth+1)
	if defs, ok := m["$defs"].(map[string]any); ok {
		for _, d := range defs {
			checkStrict(t, tool, d, depth)
		}
	}
	if list, ok := m["anyOf"].([]any); ok {
		for _, e := range list {
			checkStrict(t, tool, e, depth)
		}
	}
}

// TestToolExports_selection pins which commands become tools and which inputs become
// parameters.
func TestToolExports_selection(t *testing.T) {
	dir, _ := generateProgram(t, programConf)
	mcp := readJSON(t, filepath.Join(dir, "tools", "mcp.json")).(map[string]any)
	byName := map[string]map[string]any{}
	for _, tl := range mcp["tools"].([]any) {
		m := tl.(map[string]any)
		byName[m["name"].(string)] = m
	}
	var names []string
	for n := range byName {
		names = append(names, n)
	}
	slices.Sort(names)
	// Left out: the root and remote (groups), exec (passthrough), old (deprecated), debug
	// (hidden), admin and its subtree (agent: false), and remote login (a required flag read
	// from a file). remote sync stays: its required secret comes from the server's environment.
	want := []string{"taskr_add", "taskr_import", "taskr_list", "taskr_purge", "taskr_remote_show", "taskr_remote_sync"}
	if !slices.Equal(names, want) {
		t.Errorf("tools %v, want %v", names, want)
	}
	if d := byName["taskr_remote_sync"]["description"].(string); !strings.Contains(d, "Requires TASKR_KEY in the server's environment.") {
		t.Errorf("taskr_remote_sync description %q", d)
	}
	params := func(tool string) []string {
		props := byName[tool]["inputSchema"].(map[string]any)["properties"].(map[string]any)
		var out []string
		for k := range props {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	// list: --format is added to argv, not a parameter; -C and --help are never parameters.
	if got := params("taskr_list"); !slices.Equal(got, []string{"label", "page", "verbose"}) {
		t.Errorf("taskr_list parameters %v", got)
	}
	inv := byName["taskr_list"]["_meta"].(map[string]any)[toolsMetaKey].(map[string]any)
	if fixed, _ := inv["fixed"].([]any); len(fixed) != 1 || fixed[0] != "--format=json" {
		t.Errorf("taskr_list fixed argv %v", inv["fixed"])
	}
	if got := params("taskr_import"); !slices.Contains(got, "stdin") {
		t.Errorf("taskr_import has no stdin parameter: %v", got)
	}
	ann := byName["taskr_purge"]["annotations"].(map[string]any)
	if ann["destructiveHint"] != true || ann["idempotentHint"] != true {
		t.Errorf("taskr_purge annotations %v", ann)
	}
	if _, ok := byName["taskr_remote_show"]["annotations"].(map[string]any)["readOnlyHint"]; !ok {
		t.Error("a read command lacks readOnlyHint")
	}
}

// TestPermissions_rules checks the permission snippets: JSON and TOML parse, no rule names a
// root or group command, each Codex rule's pattern matches its own match examples and not its
// not_match ones, and the Gemini regexes compile and match the command.
func TestPermissions_rules(t *testing.T) {
	dir, _ := generateProgram(t, programConf)
	claude := readJSON(t, filepath.Join(dir, "agents", "claude-settings.json")).(map[string]any)["permissions"].(map[string]any)
	all := append(slices.Clone(claude["allow"].([]any)), claude["ask"].([]any)...)
	for _, r := range all {
		rule := r.(string)
		for _, group := range []string{"Bash(taskr *)", "Bash(taskr remote *)", "Bash(taskr admin *)"} {
			if rule == group {
				t.Errorf("a rule names the root or a group command: %s", rule)
			}
		}
	}
	allow := claude["allow"].([]any)
	if !slices.Contains(allow, any("Bash(taskr list *)")) || !slices.Contains(allow, any("Bash(taskr ls *)")) {
		t.Errorf("allow %v lacks list and its alias", allow)
	}
	if !slices.Contains(claude["ask"].([]any), any("Bash(taskr purge *)")) {
		t.Errorf("ask %v lacks purge", claude["ask"])
	}

	policy, err := os.ReadFile(filepath.Join(dir, "agents", "taskr-policy.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rule []struct {
			ToolName     string `toml:"toolName"`
			CommandRegex string `toml:"commandRegex"`
			Decision     string `toml:"decision"`
			Priority     int    `toml:"priority"`
		} `toml:"rule"`
	}
	if err := toml.Unmarshal(policy, &doc); err != nil {
		t.Fatalf("the Gemini policy doesn't parse: %v", err)
	}
	if len(doc.Rule) == 0 {
		t.Fatal("no Gemini rules")
	}
	for _, r := range doc.Rule {
		re := regexp.MustCompile(r.CommandRegex)
		if re.MatchString("taskr remote") || re.MatchString("taskr") || re.MatchString("taskr listall") {
			t.Errorf("%q matches a group, the root or a longer word", r.CommandRegex)
		}
	}

	rules, err := os.ReadFile(filepath.Join(dir, "agents", "taskr.rules"))
	if err != nil {
		t.Fatal(err)
	}
	checkCodexRules(t, string(rules))
}

// checkCodexRules runs each prefix_rule's match and not_match examples against its pattern, as
// Codex does when it loads the file.
func checkCodexRules(t *testing.T, src string) {
	t.Helper()
	ruleRE := regexp.MustCompile(`(?s)prefix_rule\(\n    pattern = (\[.*?\]),\n.*?match = \[(".*?")\],\n(?:    not_match = \[(".*?")\],\n)?\)`)
	found := ruleRE.FindAllStringSubmatch(src, -1)
	if len(found) == 0 {
		t.Fatalf("no prefix_rule in:\n%s", src)
	}
	for _, m := range found {
		var pattern []any
		if err := json.Unmarshal([]byte(m[1]), &pattern); err != nil {
			t.Fatalf("pattern %s: %v", m[1], err)
		}
		matches := func(cmdline string) bool {
			words := strings.Fields(cmdline)
			if len(words) < len(pattern) {
				return false
			}
			for i, p := range pattern {
				switch v := p.(type) {
				case string:
					if words[i] != v {
						return false
					}
				case []any:
					if !slices.Contains(v, any(words[i])) {
						return false
					}
				}
			}
			return true
		}
		var match string
		_ = json.Unmarshal([]byte(m[2]), &match)
		if !matches(match) {
			t.Errorf("pattern %s doesn't match its example %q", m[1], match)
		}
		if m[3] != "" {
			var not string
			_ = json.Unmarshal([]byte(m[3]), &not)
			if matches(not) {
				t.Errorf("pattern %s matches its not_match example %q", m[1], not)
			}
		}
	}
}

// TestSkill_format checks SKILL.md against the Agent Skills rules: frontmatter name (matching
// its directory) and description, under 500 lines, and allowed-tools naming read-only leaf
// commands only.
func TestSkill_format(t *testing.T) {
	dir, _ := generateProgram(t, programConf)
	page, err := os.ReadFile(filepath.Join(dir, "skills", "taskr", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(page), "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatalf("SKILL.md has no frontmatter:\n%s", page)
	}
	var fm struct {
		Name         string `yaml:"name"`
		Description  string `yaml:"description"`
		AllowedTools string `yaml:"allowed-tools"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		t.Fatal(err)
	}
	if fm.Name != "taskr" || len(fm.Name) > 64 || !skillName.MatchString(fm.Name) {
		t.Errorf("name %q", fm.Name)
	}
	if fm.Description == "" || len(fm.Description) > 1024 {
		t.Errorf("description %q", fm.Description)
	}
	if fm.AllowedTools != "Bash(taskr list *) Bash(taskr ls *) Bash(taskr remote show *)" {
		t.Errorf("allowed-tools %q", fm.AllowedTools)
	}
	if n := strings.Count(string(page), "\n"); n > skillLineLimit {
		t.Errorf("SKILL.md is %d lines", n)
	}
}

// TestProgramFeatures_errors pins the generate errors: a tool name too long for OpenAI, two
// commands with one tool name, and an invalid skill name.
func TestProgramFeatures_errors(t *testing.T) {
	a := &agentProgram{doc: agentDoc{Name: strings.Repeat("x", 60)}}
	a.commands = []agentCommand{{c: agentContractCmd{Path: []string{"longer"}}, leaf: true, offered: true, invocation: "x longer"}}
	tools, _, err := buildTools(a, false)
	if err != nil || len(tools) != 1 {
		t.Fatal(err)
	}
	if _, _, err := renderToolExports(&program{}, &Feature{Targets: []string{"openai-strict"}}, a, nil); err == nil || !strings.Contains(err.Error(), "longer than openai-strict allows (64 characters)") {
		t.Errorf("err %v", err)
	}
	a.commands = append(a.commands, agentCommand{c: agentContractCmd{Path: []string{"longer"}}, leaf: true, offered: true, invocation: "x other"})
	if _, _, err := buildTools(a, false); err == nil || !strings.Contains(err.Error(), "both make the tool name") {
		t.Errorf("collision err %v", err)
	}
	if _, _, err := renderSkill(&program{}, &Feature{Name: "Bad_Name"}, a, nil); err == nil {
		t.Error("an invalid skill name passed")
	}
}
