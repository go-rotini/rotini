package rotini

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cspInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Replicas int `rotini:"replicas" recon:"deploy.replicas"`
		}
	}
}

// A configuration file's schema violation names where the value is in the file and what it
// was, as a parse error in the same file names where it failed.
func TestConfigSchema_positionAndValue(t *testing.T) {
	schema := `{"type":"object","properties":{"deploy":{"type":"object","properties":{` +
		`"replicas":{"type":"integer"},"hosts":{"type":"array","items":{"type":"string"}},"token":{"type":"string"}}}}}`
	tests := []struct {
		name, format, body, want string
		keys                     []ConfigKey
	}{
		{"yaml", "yaml", "deploy:\n  replicas: three\n", `.yaml:2:13 is invalid: deploy.replicas: value is not of type integer (got "three")`, nil},
		{"json", "json", "{\n  \"deploy\": {\"replicas\": true}\n}\n", `.json:2:26 is invalid: deploy.replicas: value is not of type integer (got true)`, nil},
		{"jsonc", "jsonc", "{\n  // c\n  \"deploy\": {\n    \"replicas\": 1.5\n  }\n}\n", `.jsonc:4:17 is invalid: deploy.replicas: value is not of type integer (got 1.5)`, nil},
		{"toml", "toml", "[deploy]\nreplicas = \"three\"\n", `.toml:2:12 is invalid: deploy.replicas: value is not of type integer (got "three")`, nil},
		{"list item", "yaml", "deploy:\n  hosts:\n    - a\n    - 7\n", `.yaml:4:7 is invalid: deploy.hosts.1: value is not of type string (got 7)`, nil},
		{"secret", "yaml", "deploy:\n  token: 42\n", `.yaml:2:10 is invalid: deploy.token: value is not of type string`,
			[]ConfigKey{{Key: "deploy.token", Secret: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app."+tt.format)
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: tt.format, Schema: schema, Keys: tt.keys}}}
			var in cspInputs
			err := NewInputReader(meta).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir), &in)
			want := "app: configuration file " + filepath.Join(dir, "app") + tt.want
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant %s", err, want)
			}
			// The temp dir's random name can hold "42"; look only at the message around it.
			if strings.Contains(tt.name, "secret") && strings.Contains(strings.ReplaceAll(err.Error(), dir, ""), "42") {
				t.Errorf("err = %v, shows a secret value", err)
			}
			wantKind(t, err, channelConfig, InputErrorKindSchemaViolation)
		})
	}
}

// Under a profile, the position is the value in the selected profile's section.
func TestConfigSchema_positionInProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	body := "deploy:\n  replicas: 2\nprofiles:\n  prod:\n    deploy:\n      replicas: many\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	schema := `{"type":"object","properties":{"deploy":{"type":"object","properties":{"replicas":{"type":"integer"}}}}}`
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: "yaml", Schema: schema,
		Profiles: &ProfilesDef{Under: "profiles", Flag: "profile"}}}}
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
	err := NewInputReader(meta).Read(NewContextFor(def, []string{"--profile", "prod"}).WithDir(dir), &in)
	want := "app: configuration file " + path + `:6:17 (profile prod) is invalid: deploy.replicas: value is not of type integer (got "many")`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestLocators(t *testing.T) {
	tests := []struct {
		name   string
		locate locator
		doc    string
		path   []string
		at     string // "" = not found
	}{
		{"toml dotted key", locateTOML, "a.b = 1\n", []string{"a", "b"}, "1:7"},
		{"toml array of tables", locateTOML, "[[srv]]\nport = 1\n[[srv]]\nport = 2\n", []string{"srv", "1", "port"}, "4:8"},
		{"toml array", locateTOML, "xs = [1, # c\n  2]\n", []string{"xs", "1"}, "2:3"},
		{"toml missing", locateTOML, "a = 1\n", []string{"b"}, ""},
		{"yaml flow list", locateYAML, "a: [1, 2]\n", []string{"a", "1"}, "1:8"},
		{"yaml through a scalar", locateYAML, "a: 1\n", []string{"a", "b"}, ""},
		{"jsonc array", locateJSONC, "{\"a\": [\n  // c\n  1, 2]}", []string{"a", "1"}, "3:6"},
		{"jsonc bad", locateJSONC, "{", []string{"a"}, ""},
	}
	for _, tt := range tests {
		line, col, ok := tt.locate([]byte(tt.doc), tt.path)
		got := ""
		if ok {
			got = fmt.Sprintf("%d:%d", line, col)
		}
		if got != tt.at {
			t.Errorf("%s: at %q, want %q", tt.name, got, tt.at)
		}
	}
}
