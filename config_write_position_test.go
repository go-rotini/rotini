package rotini

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// cwpKind fails unless err is a usage *InputError of kind want for the config channel.
func cwpKind(t *testing.T, err error, want InputErrorKind) {
	t.Helper()
	ie, ok := asError[*InputError](err)
	if !ok || ie.Kind != want || ie.Channel != channelConfig || !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v (%T), want a usage *InputError of kind %v", err, err, want)
	}
}

// A write the schema rejects names where the value would be in the edited file and what it
// would be, as a read of that file names where it is.
func TestSetConfigValue_schemaPosition(t *testing.T) {
	schema := `{"type":"object","properties":{"deploy":{"type":"object","properties":{"region":{"enum":["eu","us"]}}}}}`
	tests := []struct{ name, format, body, want string }{
		{"yaml", "yaml", "# where\ndeploy:\n  region: eu\n", `:3:11 would be invalid: deploy.region: value is not in enum (got "ap")`},
		{"json", "json", "{\n  \"deploy\": {\n    \"region\": \"eu\"\n  }\n}\n", `:3:15 would be invalid: deploy.region: value is not in enum (got "ap")`},
		{"jsonc", "jsonc", "{\n  // where\n  \"deploy\": {\"region\": \"eu\"}\n}\n", `:3:24 would be invalid: deploy.region: value is not in enum (got "ap")`},
		{"toml", "toml", "# where\n[deploy]\nregion = \"eu\"\n", `:3:10 would be invalid: deploy.region: value is not in enum (got "ap")`},
		// A key the file doesn't hold yet is located where the edit would add it.
		{"new key", "yaml", "other: 1\n", `:3:11 would be invalid: deploy.region: value is not in enum (got "ap")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, "app."+tt.format, tt.body)
			f := ConfigFileDef{Name: "app", Path: path, Schema: schema, Keys: []ConfigKeyDef{{Key: "deploy.region", Type: "string"}}}
			err := SetConfigValue(cwContext(dir, nil, f), "app", "deploy.region", "ap")
			want := "config key deploy.region: configuration file " + path + tt.want
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant %s", err, want)
			}
			cwpKind(t, err, InputErrorKindSchemaViolation)
			if got := cwRead(t, path); got != tt.body {
				t.Errorf("the file changed:\n%s", got)
			}
		})
	}
}

// Under a profile, the position is the value in the profile's section.
func TestSetConfigValue_schemaPositionInProfile(t *testing.T) {
	dir := t.TempDir()
	body := "region: eu\nprofiles:\n  prod:\n    region: us\n"
	path := pfWrite(t, dir, "app.yaml", body)
	f := ConfigFileDef{
		Name: "app", Path: path, Profiles: pfProfiles(), Keys: []ConfigKeyDef{{Key: "region", Type: "string"}},
		Schema: `{"type":"object","properties":{"region":{"enum":["eu","us"]}}}`,
	}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "region", "ap", ConfigToProfile("prod"))
	want := "config key region: configuration file " + path + `:4:13 (profile prod) would be invalid: region: value is not in enum (got "ap")`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	cwpKind(t, err, InputErrorKindSchemaViolation)
}

// A secret value is located but never shown.
func TestSetConfigValue_schemaPositionSecret(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "token: abcd\n")
	f := ConfigFileDef{
		Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "token", Type: "string", Secret: true}},
		Schema: `{"type":"object","properties":{"token":{"type":"string","minLength":4}}}`,
	}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "token", "zq7", ConfigAllowSecretWrite())
	if err == nil || !strings.HasPrefix(err.Error(), "config key token: configuration file "+path+":1:8 would be invalid: token: ") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(strings.ReplaceAll(err.Error(), dir, ""), "zq7") {
		t.Errorf("err = %v, shows the secret value", err)
	}
	cwpKind(t, err, InputErrorKindSchemaViolation)
}

// A key the schema forbids is an unknown key, as a read reports it.
func TestSetConfigValue_schemaForbidsTheKey(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.toml", "region = \"eu\"\n")
	f := ConfigFileDef{
		Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}, {Key: "zone", Type: "string"}},
		Schema: `{"type":"object","properties":{"region":{"type":"string"}},"additionalProperties":false}`,
	}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "zone", "a")
	want := "config key zone: configuration file " + path + ":2:8 would have unknown key zone (keys: region)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	cwpKind(t, err, InputErrorKindUnknownKey)
	if ie, _ := asError[*InputError](err); ie.Token != "zone" || len(ie.Candidates) != 1 {
		t.Errorf("Token, Candidates = %q, %q", ie.Token, ie.Candidates)
	}
}

// A file whose format gives no positions (dotenv) is reported without one.
func TestSetConfigValue_schemaPositionDotenv(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.env", "REGION=eu\n")
	f := ConfigFileDef{
		Name: "app", Path: path, Format: "dotenv", Keys: []ConfigKeyDef{{Key: "REGION", Type: "string"}},
		Schema: `{"type":"object","properties":{"REGION":{"enum":["eu","us"]}}}`,
	}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "REGION", "ap")
	want := "config key REGION: configuration file " + filepath.Join(dir, "app.env") + ` would be invalid: REGION: value is not in enum (got "ap")`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}
