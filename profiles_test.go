package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A configuration file with profiles: one named section chosen per run by a selector input,
// its keys read as if they sat at the top of the file, over the file's shared keys.

type pfInputs struct {
	App struct {
		Flags struct {
			Help    bool   `rotini:"help"`
			Profile string `rotini:"profile" recon:"profile" env:"APP_PROFILE"`
			Region  string `rotini:"region" recon:"region" env:"APP_REGION"`
			Port    int    `rotini:"port" recon:"port"`
			Config  string `rotini:"config"`
		}
		Arguments struct{}
		Config    struct {
			Host   string            `rotini:"host" recon:"db.host"`
			DBPort int               `rotini:"db-port" recon:"db.port"`
			Tags   []string          `rotini:"tags" recon:"tags"`
			Labels map[string]string `rotini:"labels" recon:"labels"`
			Pinned string            `rotini:"pinned" recon:"pinned" cfgfile:"app"`
		}
	}
}

func pfDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
			{Name: "profile", Identifiers: []string{"-p", "--profile"}, Type: "string"},
			{Name: "region", Identifiers: []string{"--region"}, Type: "string"},
			{Name: "port", Identifiers: []string{"--port"}, Type: "int"},
			{Name: "config", Identifiers: []string{"--config"}, Type: "string"},
		},
	}
}

const pfFile = `db: {host: shared.example, port: 5432}
tags: [a, b]
labels: {team: core, tier: web}
region: shared-r
pinned: shared-pin
profile: prod
profiles:
  default: {region: default-r}
  prod:
    db: {host: prod.example}
    tags: [p]
    labels: {tier: api}
    region: prod-r
    pinned: prod-pin
  bad:
    port: not-a-number
    db: {port: nope}
`

// pfProfiles is the profiles block the tests' file declares, unless a test replaces it.
func pfProfiles() *ProfilesDef {
	return &ProfilesDef{Under: "profiles", Flag: "profile", Env: "APP_PROFILE", Default: "default"}
}

func pfWrite(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func pfSettings(files ...ConfigFile) InputSettings { return InputSettings{ConfigFiles: files} }

func pfFileAt(path string) ConfigFile {
	return ConfigFile{Name: "app", Path: path, Profiles: pfProfiles()}
}

func pfContext(dir string, env []string, meta InputSettings, argv ...string) *Context {
	return NewContextFor(pfDef(), argv).WithDir(dir).WithEnviron(env).WithInputSettings(meta)
}

func pfRead(t *testing.T, env []string, meta InputSettings, argv ...string) (pfInputs, error) {
	t.Helper()
	dir := t.TempDir()
	var in pfInputs
	err := NewInputReader(meta).Read(pfContext(dir, env, meta, argv...), &in)
	return in, err
}

func TestProfiles_selection(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		argv    []string
		def     string // ProfilesDef.Default
		region  string
		profile string // the selector flag's value
	}{
		{"default", nil, nil, "default", "default-r", ""},
		{"argv", []string{"APP_PROFILE=default"}, []string{"--profile", "prod"}, "default", "prod-r", "prod"},
		{"variable", []string{"APP_PROFILE=prod"}, nil, "default", "prod-r", "prod"},
		{"empty variable falls through", []string{"APP_PROFILE="}, nil, "default", "default-r", ""},
		{"no default: shared keys", nil, nil, "", "shared-r", ""},
		{"absent default: shared keys", nil, nil, "missing", "shared-r", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
			f := pfFileAt(path)
			f.Profiles.Default = tt.def
			in, err := pfRead(t, tt.env, pfSettings(f), tt.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if in.App.Flags.Region != tt.region || in.App.Flags.Profile != tt.profile {
				t.Errorf("region %q profile %q, want %q %q", in.App.Flags.Region, in.App.Flags.Profile, tt.region, tt.profile)
			}
		})
	}
}

func TestProfiles_mergeByLeaf(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	in, err := pfRead(t, nil, pfSettings(pfFileAt(path)), "--profile", "prod")
	if err != nil {
		t.Fatal(err)
	}
	c := in.App.Config
	if c.Host != "prod.example" || c.DBPort != 5432 {
		t.Errorf("db = %q:%d, want the profile's host and the shared port", c.Host, c.DBPort)
	}
	if !slices.Equal(c.Tags, []string{"p"}) {
		t.Errorf("tags = %v, want the profile's list, replacing the shared one", c.Tags)
	}
	if want := map[string]string{"team": "core", "tier": "api"}; !reflect.DeepEqual(c.Labels, want) {
		t.Errorf("labels = %v, want %v", c.Labels, want)
	}
	if c.Pinned != "prod-pin" {
		t.Errorf("pinned = %q, want the profile's value", c.Pinned)
	}
}

func TestProfiles_argvAndEnvBeatTheProfile(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	in, err := pfRead(t, []string{"APP_REGION=env-r"}, pfSettings(pfFileAt(path)), "--profile", "prod")
	if err != nil || in.App.Flags.Region != "env-r" {
		t.Fatalf("region = %q, %v", in.App.Flags.Region, err)
	}
	in, err = pfRead(t, []string{"APP_REGION=env-r"}, pfSettings(pfFileAt(path)), "--profile", "prod", "--region", "argv-r")
	if err != nil || in.App.Flags.Region != "argv-r" {
		t.Fatalf("region = %q, %v", in.App.Flags.Region, err)
	}
}

func TestProfiles_selectorNeverReadsTheFile(t *testing.T) {
	// The file's own top-level `profile: prod` neither selects a profile nor sets the flag.
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	in, err := pfRead(t, nil, pfSettings(pfFileAt(path)))
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Flags.Profile != "" || in.App.Flags.Region != "default-r" {
		t.Errorf("profile %q region %q, want the default profile and an unset flag", in.App.Flags.Profile, in.App.Flags.Region)
	}
}

func TestProfiles_unknown(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	tests := []struct {
		name string
		env  []string
		argv []string
		want string
	}{
		{"argv", nil, []string{"--profile", "prdo"},
			`--profile: profile "prdo" is not defined in configuration file ` + path + ` (profiles: bad, default, prod)`},
		{"short identifier", nil, []string{"-p", "prdo"},
			`-p: profile "prdo" is not defined in configuration file ` + path + ` (profiles: bad, default, prod)`},
		{"variable", []string{"APP_PROFILE=prdo"}, nil,
			`APP_PROFILE: profile "prdo" is not defined in configuration file ` + path + ` (profiles: bad, default, prod)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pfRead(t, tt.env, pfSettings(pfFileAt(path)), tt.argv...)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v\nwant %s", err, tt.want)
			}
			if !errors.Is(err, ErrUsage) {
				t.Errorf("not a usage error: %v", err)
			}
			token, candidates, ok := SuggestionFacts(err)
			if !ok || token != "prdo" || !slices.Equal(candidates, []string{"bad", "default", "prod"}) {
				t.Errorf("SuggestionFacts = %q %v %v", token, candidates, ok)
			}
		})
	}
}

func TestProfiles_unknownWithoutAFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := pfRead(t, nil, pfSettings(pfFileAt(missing)), "--profile", "prod")
	if want := `--profile: profile "prod" is not defined: no configuration file defines profiles`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	// Defaulted, it is silent.
	if _, err := pfRead(t, nil, pfSettings(pfFileAt(missing))); err != nil {
		t.Fatal(err)
	}
}

func TestProfiles_shortCircuitWaivesTheCheck(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	if _, err := pfRead(t, []string{"APP_PROFILE=nope"}, pfSettings(pfFileAt(path)), "--help"); err != nil {
		t.Fatalf("--help with a bad profile: %v", err)
	}
	// A file whose profiles don't map names to sections doesn't block --help either.
	bad := pfWrite(t, t.TempDir(), "app.yaml", "profiles: [a, b]\n")
	if _, err := pfRead(t, nil, pfSettings(pfFileAt(bad)), "--help"); err != nil {
		t.Fatalf("--help with a malformed file: %v", err)
	}
}

func TestProfiles_malformed(t *testing.T) {
	for _, body := range []string{"profiles: [a, b]\n", "profiles: {prod: 3}\n", "profiles: text\n"} {
		path := pfWrite(t, t.TempDir(), "app.yaml", body)
		_, err := pfRead(t, nil, pfSettings(pfFileAt(path)))
		want := "configuration file " + path + `: "profiles" must map profile names to sections`
		if err == nil || err.Error() != want {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	// An empty profile, or an empty profiles key, is fine.
	path := pfWrite(t, t.TempDir(), "app.yaml", "region: r\nprofiles:\n  empty:\n")
	if in, err := pfRead(t, nil, pfSettings(pfFileAt(path)), "--profile", "empty"); err != nil || in.App.Flags.Region != "r" {
		t.Fatalf("region = %q, %v", in.App.Flags.Region, err)
	}
}

func TestProfiles_valueErrorsNameTheProfile(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	_, err := pfRead(t, nil, pfSettings(pfFileAt(path)), "--profile", "bad")
	if err == nil || !strings.Contains(err.Error(), "configuration file "+path+" (profile bad), key port") {
		t.Fatalf("err = %v", err)
	}
}

func TestProfiles_schemaChecksTheEffectiveView(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	f := pfFileAt(path)
	// additionalProperties false: the profiles key itself is not part of what is checked.
	f.Schema = `{"type":"object","additionalProperties":false,"properties":{` +
		`"db":{"type":"object","properties":{"host":{"type":"string"},"port":{"type":"integer"}}},` +
		`"tags":{"type":"array"},"labels":{"type":"object"},"region":{"type":"string"},"pinned":{"type":"string"},"profile":{"type":"string"},"port":{"type":"integer"}}}`
	if _, err := pfRead(t, nil, pfSettings(f), "--profile", "prod"); err != nil {
		t.Fatalf("prod: %v", err)
	}
	// The bad profile is checked only when selected.
	if _, err := pfRead(t, nil, pfSettings(f)); err != nil {
		t.Fatalf("default: %v", err)
	}
	_, err := pfRead(t, nil, pfSettings(f), "--profile", "bad")
	if err == nil || !strings.Contains(err.Error(), "configuration file "+path+" (profile bad) is invalid") {
		t.Fatalf("bad: %v", err)
	}
}

func TestProfiles_unionAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	project := pfWrite(t, dir, "project.yaml", "profiles:\n  dev: {region: dev-r}\n")
	user := pfWrite(t, dir, "user.yaml", "profiles:\n  prod: {region: prod-r}\n")
	files := pfSettings(
		ConfigFile{Name: "app", Path: project, Profiles: pfProfiles()},
		ConfigFile{Name: "user", Path: user, Profiles: pfProfiles()},
	)
	// A profile only one file defines is not an error.
	in, err := pfRead(t, nil, files, "--profile", "prod")
	if err != nil || in.App.Flags.Region != "prod-r" {
		t.Fatalf("region = %q, %v", in.App.Flags.Region, err)
	}
	_, err = pfRead(t, nil, files, "--profile", "stage")
	want := `--profile: profile "stage" is not defined in configuration files ` + project + ", " + user + " (profiles: dev, prod)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestProfiles_configSourceAndDiscover(t *testing.T) {
	dir := t.TempDir()
	other := pfWrite(t, dir, "other.yaml", "profiles:\n  prod: {region: other-prod}\n")
	f := pfFileAt(filepath.Join(dir, "absent.yaml"))
	f.PathFrom = &PathFromDef{Flag: "config"}
	in, err := pfRead(t, nil, pfSettings(f), "--config", other, "--profile", "prod")
	if err != nil || in.App.Flags.Region != "other-prod" {
		t.Fatalf("config_source: region = %q, %v", in.App.Flags.Region, err)
	}

	home := t.TempDir()
	pfWrite(t, home, filepath.Join("xdg", "acme", "config.yaml"), "profiles:\n  prod: {region: xdg-prod}\n")
	xdg := ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "xdg", App: "acme", File: "config.yaml"}, Profiles: pfProfiles()}
	env := []string{"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"), "APP_PROFILE=prod"}
	in, err = pfRead(t, env, pfSettings(xdg))
	if err != nil || in.App.Flags.Region != "xdg-prod" {
		t.Fatalf("xdg: region = %q, %v", in.App.Flags.Region, err)
	}

	work := filepath.Join(home, "proj", "sub")
	pfWrite(t, home, filepath.Join("proj", ".acme.yaml"), "profiles:\n  prod: {region: walk-prod}\n")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	walk := ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "walk-up", File: ".acme.yaml"}, Profiles: pfProfiles()}
	meta := pfSettings(walk)
	var got pfInputs
	if err := NewInputReader(meta).Read(pfContext(work, []string{"APP_PROFILE=prod"}, meta), &got); err != nil || got.App.Flags.Region != "walk-prod" {
		t.Fatalf("walk-up: region = %q, %v", got.App.Flags.Region, err)
	}
}

func TestProfiles_dotenvSelects(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", pfFile)
	pfWrite(t, dir, ".env", "APP_PROFILE=prod\n")
	meta := pfSettings(pfFileAt(path), ConfigFile{Name: "dotenv", Path: ".env", Format: "dotenv", As: "env"})
	var in pfInputs
	if err := NewInputReader(meta).Read(pfContext(dir, nil, meta), &in); err != nil || in.App.Flags.Region != "prod-r" {
		t.Fatalf("region = %q, %v", in.App.Flags.Region, err)
	}
}

func TestProfiles_envInputSelector(t *testing.T) {
	// A selector variable the flag doesn't read itself (an env input's) still selects, and the
	// flag reports the profile in use.
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	f := pfFileAt(path)
	f.Profiles.Env = "APP_PROFILE,ACME_PROFILE"
	in, err := pfRead(t, []string{"ACME_PROFILE=prod"}, pfSettings(f))
	if err != nil || in.App.Flags.Region != "prod-r" || in.App.Flags.Profile != "prod" {
		t.Fatalf("region %q profile %q, %v", in.App.Flags.Region, in.App.Flags.Profile, err)
	}
}

func TestProfiles_report(t *testing.T) {
	path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
	meta := pfSettings(pfFileAt(path))
	in, report, err := pfContext(t.TempDir(), []string{"APP_PROFILE=prod"}, meta).InputsWithReport[pfInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Config.Host != "prod.example" || in.App.Flags.Region != "prod-r" || in.App.Flags.Profile != "prod" {
		t.Errorf("merged = %+v", in.App)
	}
	for path, want := range map[FieldPath]string{
		"App.Config.Host":   "config:app#profiles.prod.db.host",
		"App.Config.DBPort": "config:app#db.port",
		"App.Flags.Region":  "config:app#profiles.prod.region",
		"App.Flags.Profile": "env:APP_PROFILE",
	} {
		if w, ok := report.Winner(path); !ok || w.Origin != want {
			t.Errorf("%s from %q, want %q", path, w.Origin, want)
		}
	}
	if _, _, err := pfContext(t.TempDir(), []string{"APP_PROFILE=nope"}, meta).InputsWithReport[pfInputs](); err == nil || !strings.Contains(err.Error(), `profile "nope" is not defined`) {
		t.Errorf("unknown profile through the report: %v", err)
	}
}

func TestConfigProfiles(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", pfFile)
	meta := pfSettings(pfFileAt(path), ConfigFile{Name: "plain", Path: path})
	names, err := ConfigProfiles(pfContext(dir, nil, meta), "app")
	if err != nil || !slices.Equal(names, []string{"bad", "default", "prod"}) {
		t.Fatalf("names = %v, %v", names, err)
	}
	// It reads the file config_source names.
	other := pfWrite(t, dir, "other.yaml", "profiles:\n  x: {}\n")
	f := pfFileAt(path)
	f.PathFrom = &PathFromDef{Flag: "config"}
	names, err = ConfigProfiles(pfContext(dir, nil, pfSettings(f), "--config", other), "app")
	if err != nil || !slices.Equal(names, []string{"x"}) {
		t.Fatalf("config_source: names = %v, %v", names, err)
	}
	// An absent file defines none.
	if names, err := ConfigProfiles(pfContext(dir, nil, pfSettings(pfFileAt(filepath.Join(dir, "nope.yaml")))), "app"); err != nil || names != nil {
		t.Fatalf("absent: %v, %v", names, err)
	}
	if _, err := ConfigProfiles(pfContext(dir, nil, meta), "plain"); err == nil || !errors.Is(err, ErrInternal) {
		t.Errorf("a file without profiles: %v", err)
	}
	if _, err := ConfigProfiles(pfContext(dir, nil, meta), "nosuch"); err == nil || !errors.Is(err, ErrInternal) {
		t.Errorf("an unknown file: %v", err)
	}
}

func TestResolveConfigFile_selectsTheProfile(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", pfFile)
	rf, err := resolveConfigFile(pfContext(dir, []string{"APP_PROFILE=prod"}, pfSettings(pfFileAt(path))), "app")
	if err != nil {
		t.Fatal(err)
	}
	defer rf.src.Close()
	if rf.path != path || rf.profile.name != "prod" || !rf.profile.explicit {
		t.Errorf("resolved %q profile %+v", rf.path, rf.profile)
	}
}

func TestEffectiveDocument(t *testing.T) {
	m := map[string]any{
		"a":        map[string]any{"x": 1, "y": 2},
		"list":     []any{1, 2},
		"profiles": map[string]any{"p": map[string]any{"a": map[string]any{"y": 3}, "list": []any{9}}},
	}
	got := effectiveDocument(m, "profiles", "p")
	want := map[string]any{"a": map[string]any{"x": 1, "y": 3}, "list": []any{9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("effective = %v, want %v", got, want)
	}
	if got := effectiveDocument(m, "profiles", ""); !reflect.DeepEqual(got, map[string]any{"a": m["a"], "list": m["list"]}) {
		t.Errorf("no profile = %v", got)
	}
}
