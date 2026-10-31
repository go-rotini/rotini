package rotini

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// cwKeys is the key table of the test file.
func cwKeys() []ConfigKeyDef {
	return []ConfigKeyDef{
		{Key: "deploy.replicas", Type: "int", Minimum: new(1.0), Maximum: new(9.0)},
		{Key: "deploy.timeout", Type: "time.Duration"},
		{Key: "level", Type: "string", Enum: []string{"debug", "info"}, IgnoreCase: true},
		{Key: "mount", Type: "Mount", Object: true},
		{Key: "name", Type: "string", Pattern: "^[a-z]+$"},
		{Key: "owner", Type: "Owner"},
		{Key: "region", Type: "string"},
		{Key: "tags", Type: "[]string", MaxItems: new(3)},
		{Key: "token", Type: "string", Secret: true},
		{Key: "verbose", Type: "bool"},
	}
}

func cwFile(path string) ConfigFileDef {
	return ConfigFileDef{Name: "app", Path: path, Keys: cwKeys()}
}

func cwContext(dir string, env []string, files ...ConfigFileDef) *Context {
	return pfContext(dir, env, pfSettings(files...))
}

func cwRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const cwYAML = `# Settings for app.
region: eu   # where it runs

deploy:
  # how many
  replicas: 2
  timeout: 5s
tags: [a, b]
`

func TestSetConfigValue_keepsTheLayout(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", cwYAML)
	rtx := cwContext(dir, nil, cwFile(path))
	steps := []struct{ key, raw string }{
		{"region", "us"},
		{"deploy.replicas", "3"},
		{"level", "INFO"},
		{"tags", "x, y"},
		{"verbose", "yes"},
	}
	for _, s := range steps {
		if err := SetConfigValue(rtx, "app", s.key, s.raw); err != nil {
			t.Fatalf("%s=%s: %v", s.key, s.raw, err)
		}
	}
	want := `# Settings for app.
region: us   # where it runs

deploy:
  # how many
  replicas: 3
  timeout: 5s
tags: [x, "y"]
level: info
verbose: true
`
	if got := cwRead(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	if err := UnsetConfigValue(rtx, "app", "deploy.timeout"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetConfigValue(rtx, "app", "deploy.timeout"); err != nil {
		t.Fatalf("removing an absent key: %v", err)
	}
	if got := cwRead(t, path); strings.Contains(got, "timeout") || !strings.Contains(got, "  # how many\n  replicas: 3\n") {
		t.Errorf("after unset:\n%s", got)
	}
}

func TestSetConfigValue_refusesInvalidValues(t *testing.T) {
	tests := []struct {
		key, raw string
		msg      string
		token    string
	}{
		{"nosuch", "x", "unknown config key nosuch (keys: deploy.replicas, deploy.timeout, level, mount, name, owner, region, tags, token, verbose)", "nosuch"},
		{"level", "loud", `invalid value "loud" for config key level (one of: debug, info)`, "loud"},
		{"deploy.replicas", "ten", `config key deploy.replicas: "ten" is not a valid integer`, ""},
		{"deploy.replicas", "10", "config key deploy.replicas must be <= 9 (got 10)", ""},
		{"deploy.timeout", "soon", `config key deploy.timeout: "soon" is not a valid duration (write it as 90s, 5m, 1h30m or 7d)`, ""},
		{"name", "Bob", `config key name must match ^[a-z]+$ (got "Bob")`, ""},
		{"tags", "a,b,c,d", "config key tags accepts at most 3 values (got 4)", ""},
		{"verbose", "maybe", `config key verbose: "maybe" is not a valid boolean`, ""},
		{"token", "s3cr3t", "config key token is secret and isn't written to a file", ""},
		{"mount", "x", "config key mount holds an object; edit the configuration file by hand", ""},
	}
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", cwYAML)
	rtx := cwContext(dir, nil, cwFile(path))
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.raw, func(t *testing.T) {
			err := SetConfigValue(rtx, "app", tt.key, tt.raw)
			ie, ok := errors.AsType[*InputError](err)
			if !ok || !errors.Is(err, ErrUsage) {
				t.Fatalf("err = %v, want a usage *InputError", err)
			}
			if ie.Msg != tt.msg || ie.Channel != channelConfig || ie.Input != tt.key || ie.Token != tt.token {
				t.Errorf("got %+v\nwant msg %q", ie, tt.msg)
			}
		})
	}
	if got := cwRead(t, path); got != cwYAML {
		t.Errorf("a refused write changed the file:\n%s", got)
	}
}

// TestSetConfigValue_checksAsTheCommandLine runs one table through the command line's checks of
// a flag and the writer's checks of a key declared alike: the messages agree after the label.
func TestSetConfigValue_checksAsTheCommandLine(t *testing.T) {
	tests := []struct {
		key ConfigKeyDef
		raw string
	}{
		{ConfigKeyDef{Key: "n", Type: "int", Maximum: new(5.0)}, "7"},
		{ConfigKeyDef{Key: "n", Type: "int"}, "x"},
		{ConfigKeyDef{Key: "n", Type: "string", MinLength: 3}, "ab"},
		{ConfigKeyDef{Key: "n", Type: "string", Pattern: "^a", PatternMessage: "starts with a"}, "b"},
		{ConfigKeyDef{Key: "n", Type: "[]int", MinItems: 2}, "1"},
		{ConfigKeyDef{Key: "n", Type: "string", Enum: []string{"a", "b"}}, "c"},
		{ConfigKeyDef{Key: "n", Type: "rotini.ByteSize", Maximum: new(1024.0)}, "2KiB"},
		{ConfigKeyDef{Key: "n", Type: "time.Time", Layout: "2006-01-02"}, "yesterday"},
		{ConfigKeyDef{Key: "n", Type: "int", Base: basePrefixed, Maximum: new(255.0)}, "0x100"},
		{ConfigKeyDef{Key: "n", Type: "int", Base: basePrefixed}, "1__0"},
	}
	for _, tt := range tests {
		t.Run(tt.key.Type+"="+tt.raw, func(t *testing.T) {
			dir := t.TempDir()
			rtx := cwContext(dir, nil, ConfigFileDef{Name: "app", Path: filepath.Join(dir, "a.yaml"), Keys: []ConfigKeyDef{tt.key}})
			werr := SetConfigValue(rtx, "app", "n", tt.raw)
			fd := tt.key.flagDef()
			fd.Name, fd.Identifiers = "n", []string{"--n"}
			argv := []string{"--n", tt.raw}
			prtx := NewContextFor(Definition{Name: "app", Handler: "App", Flags: []FlagDef{fd}}, argv)
			store, perr := parseInto(prtx.CommandChain(), argv, prtx.argvAcq())
			if perr == nil {
				perr = validate(prtx.CommandChain(), store)
			}
			if t, ok := typeForName(fd.Type); perr == nil && ok {
				// A value the type can't hold is reported when the flag is bound.
				if err := coerceFlagValues(reflect.New(t).Elem(), fd, []string{tt.raw}, featureClock(allFeatures())); err != nil {
					perr = coerceFailure("--n", "--n", err, false)
				}
			}
			if werr == nil || perr == nil {
				t.Fatalf("write: %v, command line: %v", werr, perr)
			}
			wtail := strings.TrimPrefix(werr.Error(), "config key n")
			ptail := strings.TrimPrefix(perr.Error(), "--n")
			if strings.Contains(perr.Error(), "for --n") {
				wtail, ptail = strings.Replace(werr.Error(), "config key n", "X", 1), strings.Replace(perr.Error(), "--n", "X", 1)
			}
			if wtail != ptail {
				t.Errorf("write: %v\ncommand line: %v", werr, perr)
			}
		})
	}
}

func TestSetConfigValue_secrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "app.yaml")
	rtx := cwContext(dir, nil, cwFile(path))
	if err := SetConfigValue(rtx, "app", "token", "s3cr3t", ConfigAllowSecretWrite()); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %#o, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Errorf("new directory mode %#o, want 0700", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	err := SetConfigValue(rtx, "app", "token", "other", ConfigAllowSecretWrite())
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "is readable by others (0644); chmod 600 it first") {
		t.Errorf("a world-readable file: %v", err)
	}
	if got := cwRead(t, path); got != "token: s3cr3t\n" {
		t.Errorf("file:\n%s", got)
	}
}

func TestSetConfigValue_keepsTheMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "region: eu\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	rtx := cwContext(dir, nil, ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}}})
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %#o, want 0640", info.Mode().Perm())
	}
	fresh := filepath.Join(dir, "fresh.yaml")
	rtx = cwContext(dir, nil, ConfigFileDef{Name: "app", Path: fresh, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}}})
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); info.Mode().Perm()&^0o022 != 0o644&^0o022 {
		t.Errorf("new file mode %#o, want 0644 less the umask", info.Mode().Perm())
	}
}

func TestSetConfigValue_unchangedFileIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "region: eu\n")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(cwContext(dir, nil, cwFile(path)), "app", "region", "eu"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Errorf("an unchanged value rewrote the file")
	}
}

func TestSetConfigValue_symlink(t *testing.T) {
	dir := t.TempDir()
	target := pfWrite(t, dir, "real/app.yaml", "region: eu\n")
	link := filepath.Join(dir, "app.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symbolic links:", err)
	}
	if err := SetConfigValue(cwContext(dir, nil, cwFile(link)), "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if got := cwRead(t, target); got != "region: us\n" {
		t.Errorf("target:\n%s", got)
	}
}

func TestSetConfigValue_createsByStrategy(t *testing.T) {
	keys := []ConfigKeyDef{{Key: "region", Type: "string"}}
	t.Run("path", func(t *testing.T) {
		dir := t.TempDir()
		rtx := cwContext(dir, []string{"HOME=" + dir, "USERPROFILE=" + dir}, ConfigFileDef{Name: "app", Path: "~/a/b/app.yaml", Keys: keys})
		if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
			t.Fatal(err)
		}
		if got := cwRead(t, filepath.Join(dir, "a", "b", "app.yaml")); got != "region: us\n" {
			t.Errorf("file:\n%s", got)
		}
	})
	t.Run("xdg", func(t *testing.T) {
		dir := t.TempDir()
		xdg := filepath.Join(dir, "xdg")
		rtx := cwContext(dir, []string{"XDG_CONFIG_HOME=" + xdg, "HOME=" + dir},
			ConfigFileDef{Name: "app", Discover: &DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "app"}, Keys: keys})
		path, exists, err := ConfigFilePath(rtx, "app")
		if err != nil || exists || path != filepath.Join(xdg, "app", "config.yaml") {
			t.Fatalf("ConfigFilePath = %q, %v, %v", path, exists, err)
		}
		if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
			t.Fatal(err)
		}
		if _, exists, _ := ConfigFilePath(rtx, "app"); !exists {
			t.Errorf("the file wasn't created at %s", path)
		}
	})
	t.Run("native", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, "xdg", "app", "config.yaml")
		switch runtime.GOOS {
		case "darwin", "ios":
			want = filepath.Join(dir, "Library", "Application Support", "app", "config.yaml")
		case "windows":
			want = filepath.Join(dir, "roaming", "app", "config.yaml")
		case "plan9":
			want = filepath.Join(dir, "lib", "app", "config.yaml")
		}
		env := []string{"HOME=" + dir, "home=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "xdg"), "AppData=" + filepath.Join(dir, "roaming")}
		rtx := cwContext(dir, env, ConfigFileDef{Name: "app", Discover: &DiscoverDef{Strategy: "native", File: "config.yaml", App: "app"}, Keys: keys})
		path, exists, err := ConfigFilePath(rtx, "app")
		if err != nil || exists || path != want {
			t.Fatalf("ConfigFilePath = %q, %v, %v; want %q", path, exists, err, want)
		}
		if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
			t.Fatal(err)
		}
		if got := cwRead(t, want); got != "region: us\n" {
			t.Errorf("created:\n%s", got)
		}
	})
	t.Run("walk-up", func(t *testing.T) {
		dir := t.TempDir()
		run := filepath.Join(dir, "a", "b")
		if err := os.MkdirAll(run, 0o750); err != nil {
			t.Fatal(err)
		}
		f := ConfigFileDef{Name: "app", Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"}, Keys: keys}
		if err := SetConfigValue(cwContext(run, nil, f), "app", "region", "us"); err != nil {
			t.Fatal(err)
		}
		if got := cwRead(t, filepath.Join(run, ".app.yaml")); got != "region: us\n" {
			t.Errorf("created:\n%s", got)
		}
		pfWrite(t, dir, ".app.yaml", "region: eu\n")
		if err := os.Remove(filepath.Join(run, ".app.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := SetConfigValue(cwContext(run, nil, f), "app", "region", "us"); err != nil {
			t.Fatal(err)
		}
		if got := cwRead(t, filepath.Join(dir, ".app.yaml")); got != "region: us\n" {
			t.Errorf("the nearest file:\n%s", got)
		}
	})
	t.Run("config_source", func(t *testing.T) {
		dir := t.TempDir()
		f := ConfigFileDef{Name: "app", Path: "default.yaml", PathFrom: &PathFromDef{Flag: "config"}, Keys: keys}
		if err := SetConfigValue(cwContext(dir, nil, f), "app", "region", "us"); err == nil {
			// the flag isn't set: the fixed path
			if got := cwRead(t, filepath.Join(dir, "default.yaml")); got != "region: us\n" {
				t.Errorf("default file:\n%s", got)
			}
		} else {
			t.Fatal(err)
		}
		rtx := pfContext(dir, nil, pfSettings(f), "--config", "named/app.yaml")
		if err := SetConfigValue(rtx, "app", "region", "eu"); err != nil {
			t.Fatal(err)
		}
		if got := cwRead(t, filepath.Join(dir, "named", "app.yaml")); got != "region: eu\n" {
			t.Errorf("named file:\n%s", got)
		}
	})
	t.Run("xdg-system", func(t *testing.T) {
		dir := t.TempDir()
		f := ConfigFileDef{Name: "app", Discover: &DiscoverDef{Strategy: "xdg-system", File: "config.yaml", App: "app"}, Keys: keys}
		rtx := cwContext(dir, []string{"XDG_CONFIG_DIRS=" + dir}, f)
		err := SetConfigValue(rtx, "app", "region", "us")
		if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "system configuration directories") {
			t.Errorf("err = %v", err)
		}
		if _, _, err := ConfigFilePath(rtx, "app"); err == nil {
			t.Error("ConfigFilePath accepted a system file")
		}
	})
}

func TestSetConfigValue_profiles(t *testing.T) {
	keys := []ConfigKeyDef{{Key: "region", Type: "string"}, {Key: "db.host", Type: "string"}}
	file := func(path string) ConfigFileDef {
		return ConfigFileDef{Name: "app", Path: path, Profiles: pfProfiles(), Keys: keys}
	}
	const body = "region: eu\nprofiles:\n  prod:\n    region: us\n"
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", body)

	rtx := cwContext(dir, []string{"APP_PROFILE=prod"}, file(path))
	if err := SetConfigValue(rtx, "app", "db.host", "db.prod"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "ap", ConfigToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "af", ConfigToSharedKeys()); err != nil {
		t.Fatal(err)
	}
	want := "region: af\nprofiles:\n  prod:\n    region: us\n    db:\n      host: db.prod\n  stage:\n    region: ap\n"
	if got := cwRead(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	warns := rtx.copyWarnings()
	if len(warns) != 1 || !strings.Contains(warns[0].Error(), `config key region is set in the shared keys of configuration file `) ||
		!strings.Contains(warns[0].Error(), `but profile "prod" overrides it`) {
		t.Errorf("warnings = %v", warns)
	}

	// The default profile, which the file doesn't define, is created.
	if err := SetConfigValue(cwContext(dir, nil, file(path)), "app", "db.host", "db.default"); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); !strings.HasSuffix(got, "  default:\n    db:\n      host: db.default\n") {
		t.Errorf("file:\n%s", got)
	}

	// Removing a profile's last key keeps the profile.
	if err := UnsetConfigValue(rtx, "app", "region", ConfigToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); !strings.Contains(got, "  stage: {}\n") {
		t.Errorf("file:\n%s", got)
	}

	for _, opts := range [][]ConfigWriteOption{{ConfigToProfile("a"), ConfigToSharedKeys()}, {ConfigToProfile("")}} {
		if err := SetConfigValue(rtx, "app", "region", "x", opts...); !errors.Is(err, ErrInternal) {
			t.Errorf("contradicting options: %v", err)
		}
	}
	plain := ConfigFileDef{Name: "app", Path: path, Keys: keys}
	if err := SetConfigValue(cwContext(dir, nil, plain), "app", "region", "x", ConfigToProfile("a")); !errors.Is(err, ErrInternal) {
		t.Errorf("ToProfile on a file without profiles: %v", err)
	}
}

func TestSetConfigValue_schema(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "region: eu\n")
	f := ConfigFileDef{
		Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}},
		Schema: `{"type":"object","properties":{"region":{"enum":["eu","us"]}}}`,
	}
	rtx := cwContext(dir, nil, f)
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	err := SetConfigValue(rtx, "app", "region", "ap")
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "config key region: configuration file "+path+`:1:9 would be invalid: region: value is not in enum (got "ap")`) {
		t.Errorf("err = %v", err)
	}
	if got := cwRead(t, path); got != "region: us\n" {
		t.Errorf("file:\n%s", got)
	}
}

func TestSetConfigValue_customTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	rtx := cwContext(dir, nil, cwFile(path))
	if err := SetConfigValue(rtx, "app", "owner", "ada"); !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "pass rotini.ConfigValueCheck") {
		t.Errorf("a custom type without a parser: %v", err)
	}
	check := ConfigValueCheck(func(raw string) error {
		if !strings.Contains(raw, "@") {
			return errors.New("want an email address")
		}
		return nil
	})
	if err := SetConfigValue(rtx, "app", "owner", "ada", check); !errors.Is(err, ErrUsage) || err.Error() != "config key owner: want an email address" {
		t.Errorf("a parser's error: %v", err)
	}
	if err := SetConfigValue(rtx, "app", "owner", "ada@example.com", check); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); got != "owner: ada@example.com\n" {
		t.Errorf("file:\n%s", got)
	}
}

func TestSetConfigValue_formats(t *testing.T) {
	keys := []ConfigKeyDef{{Key: "a.b", Type: "int"}, {Key: "tags", Type: "[]string"}, {Key: "APP_NAME", Type: "string"}}
	tests := []struct {
		name, file, body, key, raw, want string
		as                               string
	}{
		{"json", "c.json", "{\n  \"x\": 1\n}\n", "a.b", "2", "{\n  \"x\": 1,\n  \"a\": {\n    \"b\": 2\n  }\n}\n", ""},
		{"jsonc", "c.jsonc", "{\n  // n\n  \"tags\": [\"q\"]\n}\n", "tags", "r,s", "{\n  // n\n  \"tags\": [\"r\", \"s\"]\n}\n", ""},
		{"toml", "c.toml", "# t\n[a]\nb = 1 # one\n", "a.b", "5", "# t\n[a]\nb = 5 # one\n", ""},
		{"dotenv", ".env", "export APP_NAME=old # n\nOTHER=1\n", "APP_NAME", "new", "export APP_NAME=new # n\nOTHER=1\n", "env"},
		{"dotenv list", ".env", "", "tags", "a,b", "tags=a,b\n", "env"},
		{"new json", "n.json", "", "a.b", "1", "{\n  \"a\": {\n    \"b\": 1\n  }\n}\n", ""},
		{"new toml", "n.toml", "", "tags", "x", "tags = [\"x\"]\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, tt.file, tt.body)
			f := ConfigFileDef{Name: "app", Path: path, As: tt.as, Keys: keys}
			if err := SetConfigValue(cwContext(dir, nil, f), "app", tt.key, tt.raw); err != nil {
				t.Fatal(err)
			}
			if got := cwRead(t, path); got != tt.want {
				t.Errorf("file:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestSetConfigValue_refusedLayout(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "base: &b\n  region: eu\nother: *b\n")
	f := ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "other.region", Type: "string"}}}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "other.region", "us")
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "edit the file by hand") {
		t.Errorf("err = %v", err)
	}
	broken := pfWrite(t, dir, "broken.yaml", "region: [\n")
	f = ConfigFileDef{Name: "app", Path: broken, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}}}
	if err := SetConfigValue(cwContext(dir, nil, f), "app", "region", "us"); err == nil || !errors.Is(err, ErrUsage) {
		t.Errorf("an unparsable file: %v", err)
	}
}

// An integer the file's format, or the reader rotini uses for it, can't hold exactly is a usage
// error that says why, and the file is left as it was. One the reader holds exactly is written.
func TestSetConfigValue_bigIntegers(t *testing.T) {
	tests := []struct {
		file, typ, raw, why string
		always              bool // refused whatever the reader holds
	}{
		{"c.yaml", "uint64", "18446744073709551615", "YAML reader can't hold", false},
		{"c.yaml", "uint64", "9223372036854775808", "YAML reader can't hold", false},
		{"c.json", "int64", "9007199254740993", "JSON can't hold", false},
		{"c.toml", "uint64", "18446744073709551615", "TOML integers are 64-bit signed", true},
	}
	for _, tt := range tests {
		t.Run(tt.file+" "+tt.raw, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, tt.file, "")
			f := ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "big", Type: tt.typ}}}
			err := SetConfigValue(cwContext(dir, nil, f), "app", "big", tt.raw)
			if err == nil {
				if tt.always {
					t.Fatal("written, want a usage error")
				}
				if got := cwRead(t, path); !strings.Contains(got, tt.raw) {
					t.Errorf("file:\n%s\nwant it to hold %s", got, tt.raw)
				}
				return
			}
			if !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), tt.why) || strings.Contains(err.Error(), "cannot be read") {
				t.Errorf("err = %v, want a usage error saying %q", err, tt.why)
			}
			if got := cwRead(t, path); got != "" {
				t.Errorf("the refused edit changed the file:\n%s", got)
			}
		})
	}
}

// A program whose only file is a .env file wires no configuration files, and still writes it.
func TestSetConfigValue_envFileOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	def := Definition{Name: "app", Handler: "App"}
	meta := pfSettings(ConfigFileDef{Name: "dotenv", As: "env", Format: "dotenv", Path: path, Keys: []ConfigKeyDef{{Key: "APP_PORT", Type: "int"}}})
	rtx := wiredFor(NewContextFor(def, nil).WithDir(dir).WithInputSettings(meta), def, meta)
	if rtx.features().config != nil {
		t.Fatal("the fixture wires configuration files")
	}
	if err := SetConfigValue(rtx, "dotenv", "APP_PORT", "80"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "dotenv", "APP_PORT", "x"); !errors.Is(err, ErrUsage) {
		t.Errorf("invalid value: %v", err)
	}
	if got := cwRead(t, path); got != "APP_PORT=80\n" {
		t.Errorf("file:\n%s", got)
	}
	if p, exists, err := ConfigFilePath(rtx, "dotenv"); err != nil || p != path || !exists {
		t.Errorf("ConfigFilePath = %q, %v, %v", p, exists, err)
	}
	if err := UnsetConfigValue(rtx, "dotenv", "APP_PORT"); err != nil || cwRead(t, path) != "" {
		t.Errorf("unset: %v, file %q", err, cwRead(t, path))
	}
	if _, _, err := ConfigFilePath(wired(NewContextFor(def, nil).WithInputSettings(meta)), "dotenv"); !isWiringError(err) {
		t.Errorf("no .env files wired: %v", err)
	}
}

func TestSetConfigValue_unknownFile(t *testing.T) {
	dir := t.TempDir()
	if err := SetConfigValue(cwContext(dir, nil, cwFile(filepath.Join(dir, "a.yaml"))), "nosuch", "region", "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v", err)
	}
}

func TestTypeForName(t *testing.T) {
	for _, name := range []string{
		"string", "count", "existingfile", "[]int", "map[string]time.Duration", "*int", "rotini.ByteSize",
		"*url.URL", "netip.Prefix", "[]rotini.Glob", "map[int]string",
	} {
		if _, ok := typeForName(name); !ok {
			t.Errorf("typeForName(%q) is unknown", name)
		}
	}
	for _, name := range []string{"DB", "map[string]any", "[]Mount", "map[string"} {
		if _, ok := typeForName(name); ok {
			t.Errorf("typeForName(%q) is known", name)
		}
	}
}

func TestSetConfigValue_everyReaderOfTheKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	f := ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{
		{Key: "n", Type: "int", Maximum: new(10.0)},
		{Key: "n", Type: "int", Minimum: new(5.0)},
	}}
	rtx := cwContext(dir, nil, f)
	for _, raw := range []string{"3", "11"} {
		if err := SetConfigValue(rtx, "app", "n", raw); !errors.Is(err, ErrUsage) {
			t.Errorf("n=%s: %v", raw, err)
		}
	}
	if err := SetConfigValue(rtx, "app", "n", "7"); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); got != "n: 7\n" {
		t.Errorf("file:\n%s", got)
	}
}

// A float32 key is written in its own shortest spelling, not widened to float64's.
func TestSetConfigValue_float32(t *testing.T) {
	for _, name := range []string{"app.yaml", "app.toml", "app.json"} {
		dir := t.TempDir()
		path := pfWrite(t, dir, name, "")
		rtx := cwContext(dir, nil, ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{{Key: "ratio", Type: "float32"}}})
		if err := SetConfigValue(rtx, "app", "ratio", "0.1"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := cwRead(t, path); !strings.Contains(got, "0.1") || strings.Contains(got, "0.100000") {
			t.Errorf("%s:\n%s\nwant ratio written as 0.1", name, got)
		}
	}
}

// A float32 value is judged and spelled as the number it was written as: CheckInputs takes
// 0.1 against a maximum of 0.1, and ArgvOf writes --ratio=0.1.
func TestFloat32_typedChecks(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float32", Maximum: new(0.1)},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Ratio float32 `rotini:"ratio"`
			}
		}
	}
	var in inputs
	in.App.Flags.Ratio = 0.1
	if err := NewContextFor(def, nil).CheckInputs(in, PresenceOf(in)); err != nil {
		t.Errorf("CheckInputs(0.1 <= 0.1): %v", err)
	}
	argv, _, err := ArgvOf(def, in, PresenceOf(in), ArgvPath())
	if err != nil || len(argv) != 1 || argv[0] != "--ratio=0.1" {
		t.Errorf("ArgvOf = %q, %v; want --ratio=0.1", argv, err)
	}
}

// UnsetConfigValue drops a mapping the removal empties, keeps the comment above the key, and
// treats a key the file doesn't hold, or a file that doesn't exist, as done.
func TestUnsetConfigValue_emptiesAndAbsences(t *testing.T) {
	dir := t.TempDir()
	keys := []ConfigKeyDef{{Key: "region", Type: "string"}, {Key: "db.host", Type: "string"}}
	path := pfWrite(t, dir, "app.yaml", "# where\nregion: eu\ndb:\n  host: h\n")
	rtx := cwContext(dir, nil, ConfigFileDef{Name: "app", Path: path, Keys: keys})
	if err := UnsetConfigValue(rtx, "app", "db.host"); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); got != "# where\nregion: eu\n" {
		t.Errorf("after removing db's only key:\n%s", got)
	}

	missing := filepath.Join(dir, "none", "app.yaml")
	rtx = cwContext(dir, nil, ConfigFileDef{Name: "app", Path: missing, Keys: keys})
	if err := UnsetConfigValue(rtx, "app", "region"); err != nil {
		t.Errorf("unset in a missing file: %v", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unset in a missing file created it: %v", err)
	}
}

// A value a schema rejects below a secret key (a list's item, a map's entry, an object's
// field) is as secret as the key's own, on a read and on a write.
func TestSecretKey_valuesBelowItStayHidden(t *testing.T) {
	keys := []ConfigKeyDef{
		{Key: "tokens", Type: "[]string", Secret: true},
		{Key: "creds", Type: "map[string]string", Secret: true},
		{Key: "db", Type: "DB", Secret: true, Object: true},
		{Key: "region", Type: "string"},
	}
	schema := `{"type":"object","properties":{
		"tokens":{"type":"array","items":{"minLength":10}},
		"creds":{"type":"object","additionalProperties":{"minLength":10}},
		"db":{"type":"object","properties":{"password":{"minLength":10}}},
		"region":{"type":"string"}}}`
	for _, c := range []struct{ name, doc, key, raw, at string }{
		{"list item", "tokens: [hunter2]\n", "tokens", "hunter2", "tokens.0"},
		{"map entry", "creds: {db: hunter2}\n", "creds", "db=hunter2", "creds.db"},
		{"object field", "db: {password: hunter2}\n", "region", "us", "db.password"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, "app.yaml", c.doc)
			f := ConfigFileDef{Name: "app", Path: path, Keys: keys, Schema: schema}
			rtx := cwContext(dir, nil, f)
			var in struct {
				App struct {
					Config struct {
						Region string `rotini:"region"`
					}
				}
			}
			err := NewInputReader(pfSettings(f)).Read(rtx, &in)
			if err == nil || !strings.Contains(err.Error(), c.at+":") || strings.Contains(err.Error(), "hunter2") {
				t.Errorf("read: %v", err)
			}
			if c.key != "region" {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = SetConfigValue(rtx, "app", c.key, c.raw, ConfigAllowSecretWrite())
			if err == nil || !strings.Contains(err.Error(), c.at+":") || strings.Contains(err.Error(), "hunter2") {
				t.Errorf("write: %v", err)
			}
		})
	}
}

// A key declared below a secret one is written as a secret.
func TestSetConfigValue_keyBelowASecretKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	rtx := cwContext(dir, nil, ConfigFileDef{Name: "app", Path: path, Keys: []ConfigKeyDef{
		{Key: "db", Type: "DB", Secret: true, Object: true},
		{Key: "db.password", Type: "string", Pattern: "^x"},
	}})
	err := SetConfigValue(rtx, "app", "db.password", "hunter2")
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindWriteRefused {
		t.Errorf("without ConfigAllowSecretWrite: %v", err)
	}
	err = SetConfigValue(rtx, "app", "db.password", "hunter2", ConfigAllowSecretWrite())
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("an invalid value: %v", err)
	}
}

// A config_source path in a format rotini doesn't know is the user's to fix, as when it's read.
func TestSetConfigValue_unknownFormat(t *testing.T) {
	dir := t.TempDir()
	pfWrite(t, dir, "app.conf", "region: eu\n")
	f := ConfigFileDef{Name: "app", Path: "default.yaml", PathFrom: &PathFromDef{Flag: "config"}, Keys: []ConfigKeyDef{{Key: "region", Type: "string"}}}
	rtx := pfContext(dir, nil, pfSettings(f), "--config", "app.conf")
	var in struct {
		App struct {
			Config struct {
				Region string `rotini:"region"`
			}
		}
	}
	rerr := NewInputReader(pfSettings(f)).Read(rtx, &in)
	werr := SetConfigValue(rtx, "app", "region", "us")
	for _, err := range []error{rerr, werr} {
		ie, ok := asError[*InputError](err)
		if !ok || !errors.Is(err, ErrUsage) || ie.Kind != InputErrorKindFileAccess || !strings.HasSuffix(ie.Msg, "app.conf: unsupported format") {
			t.Errorf("err = %v (%T)", err, err)
		}
	}
	if got := cwRead(t, filepath.Join(dir, "app.conf")); got != "region: eu\n" {
		t.Errorf("the file changed:\n%s", got)
	}
}
