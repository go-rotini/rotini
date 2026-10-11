package codegen

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const profilesSpec = `version: 0.0.0
command:
  name: acme
  env_prefix: ACME
  config_files:
    - name: user
      path: ~/.acme.yaml
      profiles: { under: profiles, select: profile }
  flags:
    - name: profile
      identifiers: [-p, --profile]
      cascading: true
      schema: { type: string, variable: ACME_PROFILE, default: default }
  env:
    - name: profile
      schema: { type: string, variable: AWS_PROFILE }
  commands:
    - name: deploy
      config_files:
        - name: team
          path: team.yaml
          profiles: { under: contexts, select: context, default: staging }
      env:
        - name: context
          schema: { type: string }
      config:
        - name: region
          schema: { type: string, enum: [eu, us] }
`

func profilesProgram(t *testing.T) *program {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, profilesSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/acme")
	if err != nil {
		t.Fatal(err)
	}
	return gp
}

func TestProfiles_renderedSettings(t *testing.T) {
	got := renderInputSettings(profilesProgram(t))
	for _, want := range []string{
		`Profiles: &rotini.ProfilesDef{Under: "profiles", Flag: "profile", Env: "ACME_PROFILE,AWS_PROFILE", Default: "default"}`,
		`Profiles: &rotini.ProfilesDef{Under: "contexts", Env: "ACME_CONTEXT", Default: "staging"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settings lack %s:\n%s", want, got)
		}
	}
}

func TestProfiles_contract(t *testing.T) {
	_, commands := contractJSON(t, profilesSpec)
	files, _ := json.Marshal(commands["acme"]["config_files"])
	want := `[{"name":"user","path":"~/.acme.yaml","profiles":{"default":"default","select":{"env":["ACME_PROFILE","AWS_PROFILE"],"flag":"profile"},"under":"profiles"}}]`
	if string(files) != want {
		t.Errorf("config_files = %s\nwant %s", files, want)
	}
	files, _ = json.Marshal(commands["acme deploy"]["config_files"])
	want = `[{"name":"team","path":"team.yaml","profiles":{"default":"staging","select":{"env":["ACME_CONTEXT"]},"under":"contexts"}}]`
	if string(files) != want {
		t.Errorf("config_files = %s\nwant %s", files, want)
	}
}

func TestProfiles_configSchema(t *testing.T) {
	gp := profilesProgram(t)
	var team scopedConfigFile
	for _, cf := range gp.configFiles {
		if cf.Name == "team" {
			team = cf
		}
	}
	doc := gp.configSchemaDoc(team)
	contexts := doc["properties"].(map[string]any)["contexts"].(map[string]any)
	if want := `Named profiles, one chosen per run by ACME_CONTEXT. A profile's keys win over the same keys at the top of the file, which every profile shares. Without a choice, "staging" is used.`; contexts["description"] != want {
		t.Errorf("description = %q", contexts["description"])
	}
	section := contexts["additionalProperties"].(map[string]any)
	if !reflect.DeepEqual(section["properties"].(map[string]any)["region"], doc["properties"].(map[string]any)["region"]) {
		t.Errorf("a profile's keys differ from the file's: %v", section)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("team.config.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc  string
		fail bool
	}{
		{`{"region": "eu", "contexts": {"prod": {"region": "us"}}}`, false},
		{`{"contexts": {"prod": {"region": "mars"}}}`, true},
		{`{"contexts": {"prod": 3}}`, true},
	} {
		if got := len(validateInstance("config", []byte(c.doc), schema)) > 0; got != c.fail {
			t.Errorf("%s: failure %v, want %v", c.doc, got, c.fail)
		}
	}
}

func TestProfiles_validates(t *testing.T) {
	err, warnings := validateInModule(t, profilesSpec, goldenConf, "")
	if err != nil || len(warnings) > 0 {
		t.Errorf("Validate = %v, warnings %v", err, warnings)
	}
	bad := strings.Replace(profilesSpec, "select: profile }", "select: profile, extra: 1 }", 1)
	if err, _ := validateInModule(t, bad, goldenConf, ""); err == nil {
		t.Error("an unknown key under profiles passed")
	}
}

// A profile selector is never read from a config file, so neither the contract, help nor a
// config file's schema gives it a key.
func TestProfiles_selectorHasNoConfigKey(t *testing.T) {
	_, commands := contractJSON(t, profilesSpec)
	for _, name := range []string{"acme", "acme deploy"} {
		for _, f := range commands[name]["flags"].([]any) {
			if f := f.(map[string]any); f["name"] == "profile" && f["config_key"] != nil {
				t.Errorf("%s: --profile has config_key %v", name, f["config_key"])
			}
		}
	}
	gp := profilesProgram(t)
	for _, row := range buildHelpData("acme", gp.rootHelp, gp.rootInputs, gp.tree, nil, nil, gp.envPrefix, gp.configReadsOf(nil)).Flags {
		if row.ConfigKey != "" {
			t.Errorf("help shows config key %q for %v", row.ConfigKey, row.Identifiers)
		}
	}
	for _, cf := range gp.configFiles {
		doc := gp.configSchemaDoc(cf)
		props := doc["properties"].(map[string]any)
		if _, ok := props["profile"]; ok {
			t.Errorf("%s: the schema has a top-level profile key", cf.Name)
		}
		if cf.Profiles == nil {
			continue
		}
		section := props[cf.Profiles.Under].(map[string]any)["additionalProperties"].(map[string]any)
		if _, ok := section["properties"].(map[string]any)["profile"]; ok {
			t.Errorf("%s: a profile section has a profile key", cf.Name)
		}
	}
}
