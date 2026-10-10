package codegen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const pagesExtraSpec = `version: 0.0.0
command:
  name: app
  env_prefix: APP
  topics:
    - name: filters
      summary: how filters work
      body: A filter is a word.
    - name: environment
      summary: the variables app reads
      generate: environment
  exit_status:
    - { code: 4, summary: refused, docs_url: "https://example.com/app/4" }
  flags:
    - name: port
      summary: listen port
      description: The port to listen on.
      stability: beta
      schema: { type: int }
    - name: host
      summary: the host
      schema: { type: string }
  arguments:
    - name: file
      summary: the file
      description: The file to read.
      schema: { type: string }
  env:
    - name: token
      summary: the token
      description: Get one from the console.
      stability: experimental
      schema: { type: string }
  config:
    - name: region
      summary: the region
      description: The default region.
      schema: { type: string }
  commands:
    - name: alpha
      summary: try things
      stability: experimental
`

// TestContract_pagesFields pins the contract's input descriptions (which become the parameter
// descriptions), stability markers, exit-code links and help topics.
func TestContract_pagesFields(t *testing.T) {
	raw, cmds := contractJSON(t, pagesExtraSpec)
	root := cmds["app"]

	for _, c := range []struct{ kind, name, description, stability string }{
		{"flags", "port", "The port to listen on.", "beta"},
		{"flags", "host", "", ""},
		{"arguments", "file", "The file to read.", ""},
		{"env", "token", "Get one from the console.", "experimental"},
		{"config", "region", "The default region.", ""},
	} {
		e := entry(t, root, c.kind, c.name)
		want := map[string]any{}
		if c.description != "" {
			want["description"] = c.description
		}
		if c.stability != "" {
			want["stability"] = c.stability
		}
		if got := facts(e, "description", "stability"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s %s: got %v, want %v", c.kind, c.name, got, want)
		}
	}

	props := root["parameters"].(map[string]any)["properties"].(map[string]any)
	for name, want := range map[string]string{"port": "The port to listen on.", "host": "the host", "file": "The file to read."} {
		if got := props[name].(map[string]any)["description"]; got != want {
			t.Errorf("parameters.%s.description = %v, want %q (the description, else the summary)", name, got, want)
		}
	}

	if got := cmds["app alpha"]["stability"]; got != "experimental" {
		t.Errorf("alpha stability = %v, want experimental", got)
	}
	if _, ok := root["stability"]; ok {
		t.Errorf("a stable command carries no stability: %v", root["stability"])
	}

	exits := root["exit_status"].([]any)
	if got := exits[0].(map[string]any)["docs_url"]; got != "https://example.com/app/4" {
		t.Errorf("exit 4 docs_url = %v", got)
	}

	var doc struct {
		Topics []map[string]any `json:"topics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"name": "filters", "summary": "how filters work", "body": "A filter is a word."},
		{"name": "environment", "summary": "the variables app reads", "generate": "environment"},
	}
	if !reflect.DeepEqual(doc.Topics, want) {
		t.Errorf("topics = %v, want %v", doc.Topics, want)
	}
}

// TestTopics_resolverAndPages checks that topics become resolver cases and page-list entries
// after the commands, with Topic set, and that the Definition lists them for completion.
func TestTopics_resolverAndPages(t *testing.T) {
	gp, err := resolveTree(decodeSpecYAML(t, pagesExtraSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	nodes := flattenFeature(gp, manFeatureDesc)
	block := buildFeatureBlock(nodes, "", manFeatureDesc, false, make([]string, len(nodes)))
	var cases []string
	for _, c := range block.Cases {
		cases = append(cases, c.PathsLiteral)
	}
	if want := []string{`""`, `"alpha"`, `"filters"`, `"environment"`}; !reflect.DeepEqual(cases, want) {
		t.Errorf("resolver cases = %v, want %v", cases, want)
	}
	var pages []string
	for _, pg := range block.Pages {
		pages = append(pages, pg.Name+map[bool]string{true: " (topic)"}[pg.Topic])
	}
	if want := []string{"app", "app-alpha", "app-filters (topic)", "app-environment (topic)"}; !reflect.DeepEqual(pages, want) {
		t.Errorf("pages = %v, want %v", pages, want)
	}
	if got := nodes[2].file; got != "app-filters.1" {
		t.Errorf("topic man file = %q, want app-filters.1", got)
	}
	if got := flattenFeature(gp, helpFeatureDesc)[2].file; got != "help_app.topic.filters.txt" {
		t.Errorf("topic help file = %q, want help_app.topic.filters.txt", got)
	}
	def := renderDefinition(gp)
	if !strings.Contains(def, `Topics: []rotini.TopicDef{`) || !strings.Contains(def, `{Name: "filters", Summary: "how filters work"}`) {
		t.Errorf("the Definition does not list the topics:\n%s", def)
	}
	if !strings.Contains(def, `DocsURL: "https://example.com/app/4"`) {
		t.Errorf("the Definition does not carry the exit code's docs_url:\n%s", def)
	}
}

// TestTopics_manCollision checks that a topic whose man page name a command's also has is
// reported, as two commands' are.
func TestTopics_manCollision(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: app
  topics:
    - { name: db-migrate, summary: migrating, body: How. }
  commands:
    - name: db
      commands:
        - name: migrate
`
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	problems := manPageCollisions(flattenFeature(gp, manFeatureDesc))
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), `"app-db-migrate"`) {
		t.Errorf("got %v, want one collision on app-db-migrate", problems)
	}
}

// TestEnvironmentTopic_sources checks every variable source the generated environment topic
// lists: env inputs and their variable files, flag and argument fallbacks, the completion
// switches and the XDG variables config discovery reads; hidden inputs and commands are left
// out.
func TestEnvironmentTopic_sources(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: app
  env_prefix: APP
  config_files:
    - { name: user, discover: { strategy: xdg, app: app, file: config.yaml } }
    - { name: system, discover: { strategy: xdg-system, app: app, file: config.yaml } }
  env:
    - { name: token, summary: the token, schema: { type: string, variable_file: APP_TOKEN_FILE } }
    - { name: secret, summary: hidden, hidden: true, schema: { type: string } }
  flags:
    - { name: port, summary: listen port, schema: { type: int, key: port } }
  commands:
    - name: get
      arguments:
        - { name: id, summary: the id, schema: { type: string, variable: APP_ID } }
    - name: ghost
      hidden: true
      env:
        - { name: boo, summary: invisible, schema: { type: string } }
`
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.conf = &Conf{Generate: &GenerateConfig{Features: []Feature{{Type: "completion", Enabled: true, Messages: "all", MessagesEnv: "APP_COMPLETION_MESSAGES"}}}}
	var got []string
	for _, r := range environmentTopicRows(gp) {
		got = append(got, r.Var+" ["+strings.Join(r.Commands, ",")+"] "+r.Summary)
	}
	want := []string{
		"APP_COMPLETION_MESSAGES [] set to 0, false or off to hide completion messages",
		"APP_ID [app get] the id",
		"APP_PORT [app] listen port",
		"APP_TOKEN [app] the token",
		"APP_TOKEN_FILE [app] names a file holding: the token",
		"XDG_CONFIG_DIRS [] the system directories configuration files are found in (default /etc/xdg)",
		"XDG_CONFIG_HOME [] the directory configuration files are found in (default ~/.config)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestEnvironmentTopic_nativeDiscovery checks that native discovery's XDG_CONFIG_HOME row says
// it applies on Linux only, and that an xdg entry beside it drops the qualifier.
func TestEnvironmentTopic_nativeDiscovery(t *testing.T) {
	for _, tt := range []struct{ strategies, want string }{
		{"[native]", "on Linux, the directory configuration files are found in (default ~/.config)"},
		{"[native, xdg]", "the directory configuration files are found in (default ~/.config)"},
	} {
		var entries strings.Builder
		for i, s := range strings.Split(strings.Trim(tt.strategies, "[]"), ", ") {
			fmt.Fprintf(&entries, "    - { name: c%d, discover: { strategy: %s, app: app, file: config.yaml } }\n", i, s)
		}
		spec := "version: 0.0.0\ncommand:\n  name: app\n  config_files:\n" + entries.String()
		gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range environmentTopicRows(gp) {
			got = append(got, r.Var+": "+r.Summary)
		}
		if want := []string{"XDG_CONFIG_HOME: " + tt.want}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rows %q, want %q", tt.strategies, got, want)
		}
	}
}

// TestRoffIndented keeps a description's paragraphs inside its option's .TP: they are joined
// with .sp, never .PP, and a leading "." stays text.
func TestRoffIndented(t *testing.T) {
	got := roffIndented("One.\n\n.Two \\ three.")
	want := "One.\n.sp\n\\&.Two \\e three."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStabilityInherited checks that a command's page carries the least stable of its own and
// its ancestors' stability, while command rows show what each declares.
func TestStabilityInherited(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: app
  commands:
    - name: alpha
      stability: experimental
      commands:
        - name: child
        - name: beta
          stability: beta
    - name: later
      stability: beta
`
	gp, err := resolveTree(decodeSpecYAML(t, spec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, n := range flattenFeature(gp, helpFeatureDesc) {
		got[n.name] = n.data.Stability
	}
	want := map[string]string{"app": "", "app alpha": "experimental", "app alpha child": "experimental", "app alpha beta": "experimental", "app later": "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("page stability = %v, want %v", got, want)
	}
}
