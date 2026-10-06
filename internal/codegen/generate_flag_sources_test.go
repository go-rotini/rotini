package codegen

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const flagSourcesSpec = `version: 0.0.0
command:
  name: app
  summary: a flag-sources fixture
  env_prefix: APP
  flags:
    - name: verbose
      summary: say more
      identifiers: [--verbose]
      cascading: true
      schema: { type: bool, key: log.verbose }
    - name: plain
      summary: argv only
      identifiers: [--plain]
      schema: { type: string }
    - name: token
      summary: a token
      identifiers: [--token]
      schema: { type: string, secret: true, variable: [GH_TOKEN, GITHUB_TOKEN] }
    - name: hidden
      summary: never shown
      identifiers: [--hidden]
      hidden: true
      schema: { type: string, key: hidden.key }
  commands:
    - name: deploy
      summary: deploy it
      config_files:
        - name: deploy
          path: ./deploy.yaml
      flags:
        - name: replicas
          summary: how many
          identifiers: [-r, --replicas]
          schema: { type: int, key: deploy.replicas, default: 1 }
        - name: region
          summary: where
          identifiers: [--region]
          schema: { type: string, key: region, variable: DEPLOY_REGION }
      commands:
        - name: canary
          summary: a canary deploy
    - name: list
      summary: list things
`

// flagSourcesProgram resolves the fixture and returns its help pages by command path.
func flagSourcesProgram(t *testing.T) (*program, map[string]templateHelpData) {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, flagSourcesSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]templateHelpData{}
	for _, n := range flattenFeature(gp, helpFeatureDesc) {
		pages[strings.Join(n.path, " ")] = n.data
	}
	return gp, pages
}

func rowFor(t *testing.T, rows []templateDocFlagRow, id string) templateDocFlagRow {
	t.Helper()
	for _, r := range rows {
		if slices.Contains(r.Identifiers, id) {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", id, rows)
	return templateDocFlagRow{}
}

// TestFlagRows_sources pins each flag row's environment variables and config key: derived
// under env_prefix, explicit and listed in lookup order, absent for an argv-only flag, and the
// key shown only on pages whose command reads a config file.
func TestFlagRows_sources(t *testing.T) {
	_, pages := flagSourcesProgram(t)
	tests := []struct {
		page, flag string
		cascading  bool
		env        []string
		key        string
	}{
		{"", "--verbose", false, []string{"APP_LOG_VERBOSE"}, ""}, // the root reads no config file
		{"", "--plain", false, nil, ""},
		{"", "--token", false, []string{"GH_TOKEN", "GITHUB_TOKEN"}, ""}, // secret: the names still show
		{"deploy", "--replicas", false, []string{"APP_DEPLOY_REPLICAS"}, "deploy.replicas"},
		{"deploy", "--region", false, []string{"DEPLOY_REGION"}, "region"},
		{"deploy", "--verbose", true, []string{"APP_LOG_VERBOSE"}, "log.verbose"},        // judged by the page it is on
		{"deploy canary", "--verbose", true, []string{"APP_LOG_VERBOSE"}, "log.verbose"}, // an ancestor's file counts
		{"list", "--verbose", true, []string{"APP_LOG_VERBOSE"}, ""},                     // an off-branch file does not
	}
	for _, tt := range tests {
		t.Run(tt.page+" "+tt.flag, func(t *testing.T) {
			rows := pages[tt.page].Flags
			if tt.cascading {
				rows = pages[tt.page].Cascading
			}
			row := rowFor(t, rows, tt.flag)
			if !slices.Equal(row.Env, tt.env) || row.ConfigKey != tt.key {
				t.Errorf("Env %v, ConfigKey %q; want %v, %q", row.Env, row.ConfigKey, tt.env, tt.key)
			}
		})
	}
	for _, r := range pages[""].Flags {
		if slices.Contains(r.Identifiers, "--hidden") {
			t.Error("a hidden flag has a help row")
		}
	}
}

// TestFlagRows_envMatchesTheGeneratedTag pins that help names exactly the variables the
// generated field's env tag binds, so the two cannot drift apart.
func TestFlagRows_envMatchesTheGeneratedTag(t *testing.T) {
	gp, pages := flagSourcesProgram(t)
	check := func(in *Inputs, page string) {
		t.Helper()
		if in == nil {
			return
		}
		for i, field := range flagFields(in, gp.envPrefix) {
			f := in.Flags[i]
			if f.Hidden {
				continue
			}
			row := rowFor(t, pages[page].Flags, flagIdentifiers(f)[len(flagIdentifiers(f))-1])
			var tag []string
			if field.EnvVar != "" {
				tag = strings.Split(field.EnvVar, ",")
			}
			if !slices.Equal(row.Env, tag) {
				t.Errorf("%s on %q: help shows %v, the env tag binds %v", f.Name, page, row.Env, tag)
			}
		}
	}
	check(gp.rootInputs, "")
	for _, n := range gp.tree {
		check(n.inputs, n.name)
	}
}

// TestFlagSources_render pins the line help, man and markdown draw under a flag with a
// fallback, and that a flag without one renders as before.
func TestFlagSources_render(t *testing.T) {
	_, pages := flagSourcesProgram(t)
	data := pages["deploy"]
	data.Headings = resolveHeadings(cmdHelp{})

	help, err := parseDocTemplate("help", templateHelp)
	if err != nil {
		t.Fatal(err)
	}
	page, err := renderDocText(help, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  -r, --replicas int    how many (default 1)\n                        env: APP_DEPLOY_REPLICAS · config: deploy.replicas\n",
		"  --region string       where\n                        env: DEPLOY_REGION · config: region\n",
		"  --verbose    say more\n               env: APP_LOG_VERBOSE · config: log.verbose",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("help is missing\n%s\n--- page ---\n%s", want, page)
		}
	}

	man, err := parseDocTemplate("man", templateMan)
	if err != nil {
		t.Fatal(err)
	}
	if page, err = renderManText(man, data); err != nil {
		t.Fatal(err)
	}
	if want := "how many (default 1)\n.br\nenv: \\fBAPP_DEPLOY_REPLICAS\\fR \\(md config: \\fBdeploy.replicas\\fR\n"; !strings.Contains(page, want) {
		t.Errorf("man is missing\n%s\n--- page ---\n%s", want, page)
	}

	md, err := parseDocTemplate("markdown", templateMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if page, err = renderDocText(md, data); err != nil {
		t.Fatal(err)
	}
	if want := "- `-r, --replicas` `int` — how many (default `1`); env `APP_DEPLOY_REPLICAS`, config `deploy.replicas`"; !strings.Contains(page, want) {
		t.Errorf("markdown is missing\n%s\n--- page ---\n%s", want, page)
	}

	root := pages[""]
	root.Headings = resolveHeadings(cmdHelp{})
	if page, err = renderDocText(help, root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "  --token string    a token\n                    env: GH_TOKEN, GITHUB_TOKEN") {
		t.Errorf("the root shows no config key and lists every variable\n%s", page)
	}
	if !strings.Contains(page, "  --plain string    argv only\n  --token") {
		t.Errorf("an argv-only flag got a sources line\n%s", page)
	}
}

// TestContract_flagSources pins that the contract carries the same names as help.
func TestContract_flagSources(t *testing.T) {
	gp, _ := flagSourcesProgram(t)
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name      string   `json:"name"`
				Env       []string `json:"env"`
				ConfigKey string   `json:"config_key"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range parsed.Commands {
		for _, f := range c.Flags {
			got[c.Name+" "+f.Name] = strings.Join(f.Env, ",") + "|" + f.ConfigKey
		}
	}
	for k, want := range map[string]string{
		"app plain":           "|",
		"app token":           "GH_TOKEN,GITHUB_TOKEN|",
		"app deploy replicas": "APP_DEPLOY_REPLICAS|deploy.replicas",
		"app deploy verbose":  "APP_LOG_VERBOSE|log.verbose",
		"app list verbose":    "APP_LOG_VERBOSE|",
	} {
		if got[k] != want {
			t.Errorf("%s: %q, want %q", k, got[k], want)
		}
	}
}

// TestContract_shortCircuit pins that the contract marks a short-circuit flag.
func TestContract_shortCircuit(t *testing.T) {
	gp, err := resolveTree(decodeSpecYAML(t, "version: 0.0.0\ncommand:\n  name: app\n  flags:\n    - name: help\n      identifiers: [--help]\n      short_circuit: true\n      schema: { type: bool }\n"), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"short_circuit": true`) {
		t.Errorf("contract doesn't mark --help short-circuit:\n%s", doc)
	}
}
