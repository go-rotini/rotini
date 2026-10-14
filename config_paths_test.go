package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
)

type configPathInputs struct {
	App struct {
		Flags struct {
			Config string `rotini:"config"`
		}
		Config struct {
			Name string `rotini:"name" recon:"name"`
		}
	}
}

func configPathDef(dflt string) Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "config", Identifiers: []string{"--config"}, Type: "string", Default: dflt}}}
}

func configPathSettings() InputSettings {
	return InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Path: "none.yaml", PathFrom: &PathFromDef{Flag: "config", Env: "APP_CONFIG"}}}}
}

// A config_source path the user gave, on the command line or in a variable, is the file read
// and written, as written apart from a leading ~ (which a shell leaves alone after "=" and in a
// variable set outside a shell). The flag's declared default is expanded like a declared path.
func TestConfigSource_userPathIsLiteral(t *testing.T) {
	dir := t.TempDir()
	lit := filepath.Join(dir, "a$X.yaml")
	writeTestFile(t, lit, "name: literal\n")
	writeTestFile(t, filepath.Join(dir, "ab.yaml"), "name: expanded\n")
	writeTestFile(t, filepath.Join(dir, "dflt.yaml"), "name: default\n")
	env := []string{"HOME=" + dir, "USERPROFILE=" + dir, "X=b"}

	rtx := NewContextFor(configPathDef(""), []string{"--config", lit}).WithInputSettings(configPathSettings()).WithEnviron(env)
	if in, err := rtx.Inputs[configPathInputs](); err != nil || in.App.Config.Name != "literal" {
		t.Errorf("--config %q: name %q, err %v; want the file as named", lit, in.App.Config.Name, err)
	}
	rtx = NewContextFor(configPathDef(""), nil).WithInputSettings(configPathSettings()).WithEnviron(append(env, "APP_CONFIG="+lit))
	if in, err := rtx.Inputs[configPathInputs](); err != nil || in.App.Config.Name != "literal" {
		t.Errorf("APP_CONFIG=%q: name %q, err %v; want the file as named", lit, in.App.Config.Name, err)
	}
	rtx = NewContextFor(configPathDef("$HOME/dflt.yaml"), nil).WithInputSettings(configPathSettings()).WithEnviron(env)
	if in, err := rtx.Inputs[configPathInputs](); err != nil || in.App.Config.Name != "default" {
		t.Errorf("default $HOME/dflt.yaml: name %q, err %v; want it expanded", in.App.Config.Name, err)
	}

	rtx = NewContextFor(configPathDef(""), []string{"--config", lit}).WithInputSettings(configPathSettings()).WithEnviron(env)
	if got, _, err := ConfigFilePath(rtx, "c"); err != nil || got != lit {
		t.Errorf("ConfigFilePath = %q, %v; want %q", got, err, lit)
	}

	writeTestFile(t, filepath.Join(dir, "home.yaml"), "name: home\n")
	for _, argv := range [][]string{{"--config=~/home.yaml"}, {"--config", "~/home.yaml"}} {
		rtx := NewContextFor(configPathDef(""), argv).WithInputSettings(configPathSettings()).WithEnviron(env).WithDir(t.TempDir())
		if in, err := rtx.Inputs[configPathInputs](); err != nil || in.App.Config.Name != "home" {
			t.Errorf("%q: name %q, err %v; want ~ expanded", argv, in.App.Config.Name, err)
		}
	}
	rtx = NewContextFor(configPathDef(""), nil).WithInputSettings(configPathSettings()).WithEnviron(append(env, "APP_CONFIG=~/home.yaml")).WithDir(t.TempDir())
	if in, err := rtx.Inputs[configPathInputs](); err != nil || in.App.Config.Name != "home" {
		t.Errorf("APP_CONFIG=~/home.yaml: name %q, err %v; want ~ expanded", in.App.Config.Name, err)
	}

	rtx = wired(NewContextFor(configPathDef(""), []string{"--config", "~nosuchuser/x.yaml"}).WithInputSettings(configPathSettings()).WithDir(dir).WithEnviron(env), ConfigFiles(), YAML())
	if _, err := rtx.Inputs[configPathInputs](); err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "~nosuchuser/x.yaml")+": no such file") {
		t.Errorf("~nosuchuser without path expansion: %v; want the path kept as written", err)
	}

	for _, arg := range []string{"~nosuchuser/x.yaml", "${NOPE:?boom}.yaml", "~/$X.yaml"} {
		rtx := NewContextFor(configPathDef(""), []string{"--config", arg}).WithInputSettings(configPathSettings()).WithDir(dir).WithEnviron(env)
		_, err := rtx.Inputs[configPathInputs]()
		want := filepath.Join(dir, arg) + ": no such file"
		switch {
		case strings.HasPrefix(arg, "~/"):
			want = filepath.Join(dir, arg[2:]) + ": no such file"
		case strings.HasPrefix(arg, "~nosuchuser"):
			want = `cannot expand "~nosuchuser": no such user`
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--config %q: %v; want %q", arg, err, want)
		}
	}
}

// A path naming a user that doesn't exist, and a TOML key given twice, are reported in
// rotini's words, with the line and column of the repeated key.
func TestConfigFile_problemWording(t *testing.T) {
	dir := t.TempDir()
	set := InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Path: "~nosuchuser/x.yaml"}}}
	_, err := NewContextFor(configPathDef(""), nil).WithInputSettings(set).Inputs[configPathInputs]()
	if err == nil || !strings.Contains(err.Error(), `cannot expand "~nosuchuser": no such user`) || strings.Contains(err.Error(), "user: unknown") {
		t.Errorf("~nosuchuser: %v", err)
	}

	p := filepath.Join(dir, "c.toml")
	writeTestFile(t, p, "name = 'a'\nname = 'b'\n")
	set = InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Path: p}}}
	_, err = NewContextFor(configPathDef(""), nil).WithInputSettings(set).Inputs[configPathInputs]()
	// The column is the TOML reader's: where it places the repeated key.
	want := regexp.MustCompile(`could not parse configuration file ` + regexp.QuoteMeta(p) + `:2:[1-9][0-9]*: duplicate key "name"`)
	if err == nil || !want.MatchString(err.Error()) || strings.Contains(err.Error(), "recon:") {
		t.Errorf("duplicate key: %v; want a match for %s", err, want)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type pinnedExpandInputs struct {
	App struct {
		Config struct {
			Dir  string   `rotini:"dir" recon:"dir" cfgfile:"user" expand:"home"`
			Dirs []string `rotini:"dirs" recon:"dirs" cfgfile:"user" expand:"home"`
			Rel  string   `rotini:"rel" recon:"rel" cfgfile:"user" relativeto:"config"`
		}
	}
}

// A config input pinned to one file is expanded the same way by every reader.
func TestPinnedConfig_expandedInEveryReader(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	user := filepath.Join(dir, "sub", "user.yaml")
	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, user, "dir: ~/x\ndirs: [~/a, ~/b]\nrel: data\n")
	set := InputSettings{ConfigFiles: []ConfigFile{{Name: "user", Path: user}}}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithInputSettings(set).WithEnviron([]string{"HOME=" + home, "USERPROFILE=" + home})
	// ~ is replaced by the home directory as written, so the rest keeps its slash on Windows too.
	want := home + "/x " + home + "/a," + home + "/b " + filepath.Join(dir, "sub", "data")
	show := func(in pinnedExpandInputs) string {
		c := in.App.Config
		return c.Dir + " " + strings.Join(c.Dirs, ",") + " " + c.Rel
	}
	in, err := rtx.Inputs[pinnedExpandInputs]()
	if err != nil || show(in) != want {
		t.Errorf("Inputs: %q, %v; want %q", show(in), err, want)
	}
	in, _, err = rtx.InputsWithReport[pinnedExpandInputs]()
	if err != nil || show(in) != want {
		t.Errorf("InputsWithReport: %q, %v; want %q", show(in), err, want)
	}
	layer, err := rtx.FileInputs[pinnedExpandInputs]()
	if err != nil || show(layer.Values) != want {
		t.Errorf("FileInputs: %q, %v; want %q", show(layer.Values), err, want)
	}
}

// countingCodec is YAML under its own name and extension, counting the documents it decodes.
type countingCodec struct{ decodes *int }

func (countingCodec) Name() string         { return "count" }
func (countingCodec) Extensions() []string { return []string{".count"} }
func (c countingCodec) Decode(data []byte) (map[string]any, error) {
	*c.decodes++
	return recon.YAML.Decode(data)
}
func (countingCodec) Encode(v map[string]any) ([]byte, error) { return recon.YAML.Encode(v) }

// One read of the inputs decodes each configuration file once, however many registries it
// builds (flag fallbacks, config inputs) and with a schema to check.
func TestConfigFile_decodedOncePerRead(t *testing.T) {
	type in struct {
		App struct {
			Flags struct {
				Port int `rotini:"port" recon:"port"`
			}
			Config struct {
				Name string `rotini:"name" recon:"name"`
			}
		}
	}
	p := filepath.Join(t.TempDir(), "c.count")
	writeTestFile(t, p, "name: x\nport: 8\n")
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "port", Identifiers: []string{"--port"}, Type: "int"}}}
	set := InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Path: p, Schema: `{"type":"object"}`}}}
	decodes := 0
	rtx := NewContextFor(def, nil).WithInputSettings(set)
	counting := Feature{name: "codec:count", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{name: "count", codec: func() recon.Codec { return countingCodec{&decodes} }})
	}}
	rtx.clock.feats = newFeatureSet([]Feature{ConfigFiles(), Fallbacks(), Schemas(), counting})
	got, err := rtx.Inputs[in]()
	if err != nil || got.App.Config.Name != "x" || got.App.Flags.Port != 8 {
		t.Fatalf("Inputs = %+v, %v", got.App, err)
	}
	if decodes != 1 {
		t.Errorf("decoded %d times, want 1: the schema check reads the document the file source decoded", decodes)
	}
}

// A config_source flag given an empty path is a usage error, for reading and for writing, not a
// fall back to the variable or the default; a directory given as the path says so, whatever its
// name.
func TestConfigSource_emptyPathAndDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "dflt.yaml"), "name: default\n")
	env := []string{"APP_CONFIG=" + filepath.Join(dir, "dflt.yaml")}
	for _, argv := range [][]string{{"--config="}, {"--config", ""}} {
		rtx := NewContextFor(configPathDef(filepath.Join(dir, "dflt.yaml")), argv).WithInputSettings(configPathSettings()).WithEnviron(env)
		_, err := rtx.Inputs[configPathInputs]()
		if err == nil || err.Error() != "--config was given an empty path; name a configuration file" || !errors.Is(err, ErrUsage) {
			t.Errorf("%q: %v", argv, err)
		}
		if _, _, err := ConfigFilePath(rtx, "c"); err == nil || !errors.Is(err, ErrUsage) {
			t.Errorf("%q ConfigFilePath: %v", argv, err)
		}
	}
	for _, name := range []string{"dd", "dd.yaml"} {
		sub := filepath.Join(dir, name)
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		rtx := NewContextFor(configPathDef(""), []string{"--config", sub}).WithInputSettings(configPathSettings())
		_, err := rtx.Inputs[configPathInputs]()
		if want := "could not open configuration file " + sub + ": is a directory"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--config %s: %v, want %q", name, err, want)
		}
	}
}
