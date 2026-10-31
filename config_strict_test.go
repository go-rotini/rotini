package rotini

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

const csSchema = `{"type":"object","additionalProperties":false,"properties":{` +
	`"deploy":{"$ref":"#/definitions/Deploy"},"name":{"type":"string"}},` +
	`"definitions":{"Deploy":{"type":"object","additionalProperties":false,"properties":{"replicas":{"type":"integer"},"timeout":{"type":"string"}}}}}`

type csInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Replicas int `rotini:"replicas" recon:"deploy.replicas"`
		}
	}
}

// A key an object's schema doesn't allow (additionalProperties: false) is a usage error naming
// where it is in the file and the keys that are allowed.
func TestConfigSchema_unknownKey(t *testing.T) {
	tests := []struct {
		name, file, body, want, token string
		candidates                    []string
	}{
		{"nested yaml", "app.yaml", "name: x\ndeploy:\n  replicaz: 3\n", `.yaml:3:13 has unknown key deploy.replicaz (keys: replicas, timeout)`,
			"replicaz", []string{"replicas", "timeout"}},
		{"top-level json", "app.json", "{\n  \"nmae\": \"x\"\n}\n", `.json:2:11 has unknown key nmae (keys: deploy, name)`,
			"nmae", []string{"deploy", "name"}},
		{"toml", "app.toml", "[deploy]\nreplicas = 1\nretries = 2\n", `.toml:3:11 has unknown key deploy.retries (keys: replicas, timeout)`,
			"retries", []string{"replicas", "timeout"}},
		{"author codec", "app.ini", "[deploy]\nreplicaz = 3\n", `.ini:2:12 has unknown key deploy.replicaz (keys: replicas, timeout)`,
			"replicaz", []string{"replicas", "timeout"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, tt.file, tt.body)
			files := []ConfigFile{{Name: "app", Path: path, Schema: csSchema}}
			rtx := wired(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files}),
				ConfigFiles(), Schemas(), YAML(), JSON(), TOML(), iniCodec())
			var in csInputs
			err := NewInputReader(InputSettings{ConfigFiles: files}).Read(rtx, &in)
			want := "app: configuration file " + strings.TrimSuffix(path, "."+strings.Split(tt.file, ".")[1]) + tt.want
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant %s", err, want)
			}
			wantKind(t, err, channelConfig, InputErrorKindUnknownKey)
			token, candidates, ok := SuggestionFacts(err)
			if !ok || token != tt.token || !slices.Equal(candidates, tt.candidates) {
				t.Errorf("SuggestionFacts = %q, %q, %v", token, candidates, ok)
			}
		})
	}
}

// Without additionalProperties: false, an unknown key is allowed, as before.
func TestConfigSchema_unknownKeyAllowedByDefault(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "deploy:\n  replicas: 2\n  replicaz: 3\n")
	schema := `{"type":"object","properties":{"deploy":{"type":"object","properties":{"replicas":{"type":"integer"}}}}}`
	files := []ConfigFile{{Name: "app", Path: path, Schema: schema}}
	var in csInputs
	if err := NewInputReader(InputSettings{ConfigFiles: files}).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir), &in); err != nil {
		t.Fatal(err)
	}
	if in.App.Config.Replicas != 2 {
		t.Errorf("replicas = %d", in.App.Config.Replicas)
	}
}

// Under a profile, the error names the profile, and the key is found in its section.
func TestConfigSchema_unknownKeyInProfile(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "deploy:\n  replicas: 2\nprofiles:\n  prod:\n    deploy:\n      replicaz: 9\n")
	files := []ConfigFile{{Name: "app", Path: path, Schema: csSchema, Profiles: &ProfilesDef{Under: "profiles", Flag: "profile"}}}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "profile", Identifiers: []string{"--profile"}, Type: "string"}}}
	var in struct {
		App struct {
			Flags struct {
				Profile string `rotini:"profile"`
			}
			Arguments struct{}
			Config    struct {
				Replicas int `rotini:"replicas" recon:"deploy.replicas"`
			}
		}
	}
	err := NewInputReader(InputSettings{ConfigFiles: files}).Read(NewContextFor(def, []string{"--profile", "prod"}).WithDir(dir), &in)
	want := "app: configuration file " + path + `:6:17 (profile prod) has unknown key deploy.replicaz (keys: replicas, timeout)`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestSchemaKeysAt(t *testing.T) {
	schema := `{"type":"object","properties":{"list":{"type":"array","items":{"$ref":"#/definitions/I"}}},` +
		`"definitions":{"I":{"$ref":"#/definitions/J"},"J":{"type":"object","properties":{"b":{},"a":{}}}}}`
	tests := []struct {
		path []string
		want []string
	}{
		{nil, []string{"list"}},
		{[]string{"list", "0"}, []string{"a", "b"}},
		{[]string{"nope"}, nil},
	}
	for _, tt := range tests {
		if got := schemaKeysAt(schema, tt.path); !slices.Equal(got, tt.want) {
			t.Errorf("schemaKeysAt(%v) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if got := schemaKeysAt("not json", nil); got != nil {
		t.Errorf("schemaKeysAt(bad) = %v", got)
	}
	loop := `{"$ref":"#/definitions/A","definitions":{"A":{"$ref":"#/definitions/A"}}}`
	if got := schemaKeysAt(loop, nil); got != nil {
		t.Errorf("schemaKeysAt(loop) = %v", got)
	}
}

type urInputs struct {
	App struct {
		Flags struct {
			Profile string `rotini:"profile"`
		}
		Arguments struct{}
		Config    struct {
			Replicas int               `rotini:"replicas" recon:"deploy.replicas"`
			Labels   map[string]string `rotini:"labels" recon:"labels"`
		}
	}
}

// UnreadConfigKeys lists the keys no input reads, with where they are, and nothing else.
func TestInputReport_UnreadConfigKeys(t *testing.T) {
	dir := t.TempDir()
	yamlPath := pfWrite(t, dir, "app.yaml", "deploy:\n  replicaz: 3\n  replicas: 2\nlabels:\n  team: a\nextra: [1, 2]\n"+
		"profiles:\n  prod:\n    deploy:\n      timeout: 5s\n")
	iniPath := pfWrite(t, dir, "more.ini", "[deploy]\nreplicas = 4\nzone = b\n")
	envPath := pfWrite(t, dir, ".env", "OTHER_TOOL=1\n")
	keys := []ConfigKey{{Key: "deploy.replicas", Type: "int"}, {Key: "labels", Type: "map[string]string"}}
	files := []ConfigFile{
		{Name: "app", Path: yamlPath, Keys: keys, Profiles: &ProfilesDef{Under: "profiles", Flag: "profile"}},
		{Name: "more", Path: iniPath, Keys: keys},
		{Name: "dotenv", Path: envPath, Format: "dotenv", As: "env"},
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "profile", Identifiers: []string{"--profile"}, Type: "string"}}}
	rtx := NewContextFor(def, nil).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files})
	rtx = wired(rtx, ConfigFiles(), Profiles(), EnvFiles(), Env(), Dotenv(), YAML(), iniCodec())
	_, report, err := rtx.InputsWithReport[urInputs]()
	if err != nil {
		t.Fatal(err)
	}
	got := report.UnreadConfigKeys()
	want := []UnreadConfigKey{
		{File: "app", Path: yamlPath, Key: "deploy.replicaz", Line: 2, Column: 13},
		{File: "app", Path: yamlPath, Key: "extra", Line: 6, Column: 8},
		{File: "app", Path: yamlPath, Key: "profiles.prod.deploy.timeout", Line: 10, Column: 16},
		{File: "more", Path: iniPath, Key: "deploy.zone", Line: 3, Column: 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnreadConfigKeys =\n%+v\nwant\n%+v", got, want)
	}
	if (InputReport{}).UnreadConfigKeys() != nil {
		t.Error("the zero report has unread keys")
	}
}

// A format with no positions gives none, and a file nobody reads is never opened.
func TestInputReport_UnreadConfigKeysNoPositions(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.cfg", "[deploy]\nzone = b\n")
	files := []ConfigFile{{Name: "app", Path: path, Format: "plain-ini", Keys: []ConfigKey{{Key: "deploy.replicas"}}}}
	rtx := wired(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files}),
		ConfigFiles(), Codec("plain-ini", decodeINI))
	_, report, err := rtx.InputsWithReport[csInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if got := report.UnreadConfigKeys(); !reflect.DeepEqual(got, []UnreadConfigKey{{File: "app", Path: path, Key: "deploy.zone"}}) {
		t.Errorf("UnreadConfigKeys = %+v", got)
	}
	_, report, err = rtx.InputsWithReport[struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
		}
	}]()
	if err != nil || report.UnreadConfigKeys() != nil {
		t.Errorf("a command that reads no configuration: %v, %+v", err, report.UnreadConfigKeys())
	}
}

// A stdin document's key that its schema forbids reads as an unknown key.
func TestStdin_unknownKey(t *testing.T) {
	var in stDocInputs
	rtx := stRTX(strings.NewReader(`{"name":"a","nmae":"b"}`))
	meta := InputSettings{StdinSchemas: map[string]string{"stDocStdin": `{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"}}}`}}
	err := NewInputReader(meta).Read(rtx, &in)
	if err == nil || !strings.HasSuffix(err.Error(), `stdin field "nmae": unknown key`) {
		t.Fatalf("err = %v", err)
	}
}
