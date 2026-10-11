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
func cwKeys() []ConfigKey {
	return []ConfigKey{
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

func cwFile(path string) ConfigFile {
	return ConfigFile{Name: "app", Path: path, Keys: cwKeys()}
}

func cwContext(dir string, env []string, files ...ConfigFile) *Context {
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
tags: [x, y]
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
		{"deploy.timeout", "soon", `config key deploy.timeout: "soon" is not a valid duration`, ""},
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
		key ConfigKey
		raw string
	}{
		{ConfigKey{Key: "n", Type: "int", Maximum: new(5.0)}, "7"},
		{ConfigKey{Key: "n", Type: "int"}, "x"},
		{ConfigKey{Key: "n", Type: "string", MinLength: 3}, "ab"},
		{ConfigKey{Key: "n", Type: "string", Pattern: "^a", PatternMessage: "starts with a"}, "b"},
		{ConfigKey{Key: "n", Type: "[]int", MinItems: 2}, "1"},
		{ConfigKey{Key: "n", Type: "string", Enum: []string{"a", "b"}}, "c"},
		{ConfigKey{Key: "n", Type: "rotini.ByteSize", Maximum: new(1024.0)}, "2KiB"},
		{ConfigKey{Key: "n", Type: "time.Time", Layout: "2006-01-02"}, "yesterday"},
	}
	for _, tt := range tests {
		t.Run(tt.key.Type+"="+tt.raw, func(t *testing.T) {
			dir := t.TempDir()
			rtx := cwContext(dir, nil, ConfigFile{Name: "app", Path: filepath.Join(dir, "a.yaml"), Keys: []ConfigKey{tt.key}})
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
				if err := coerceFlagValues(reflect.New(t).Elem(), fd, []string{tt.raw}, nil); err != nil {
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
	if err := SetConfigValue(rtx, "app", "token", "s3cr3t", AllowSecretWrite()); err != nil {
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
	err := SetConfigValue(rtx, "app", "token", "other", AllowSecretWrite())
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
	rtx := cwContext(dir, nil, ConfigFile{Name: "app", Path: path, Keys: []ConfigKey{{Key: "region", Type: "string"}}})
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %#o, want 0640", info.Mode().Perm())
	}
	fresh := filepath.Join(dir, "fresh.yaml")
	rtx = cwContext(dir, nil, ConfigFile{Name: "app", Path: fresh, Keys: []ConfigKey{{Key: "region", Type: "string"}}})
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
	keys := []ConfigKey{{Key: "region", Type: "string"}}
	t.Run("path", func(t *testing.T) {
		dir := t.TempDir()
		rtx := cwContext(dir, []string{"HOME=" + dir}, ConfigFile{Name: "app", Path: "~/a/b/app.yaml", Keys: keys})
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
			ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "app"}, Keys: keys})
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
		rtx := cwContext(dir, env, ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "native", File: "config.yaml", App: "app"}, Keys: keys})
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
		f := ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"}, Keys: keys}
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
		f := ConfigFile{Name: "app", Path: "default.yaml", PathFrom: &PathFromDef{Flag: "config"}, Keys: keys}
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
		f := ConfigFile{Name: "app", Discover: &DiscoverDef{Strategy: "xdg-system", File: "config.yaml", App: "app"}, Keys: keys}
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
	keys := []ConfigKey{{Key: "region", Type: "string"}, {Key: "db.host", Type: "string"}}
	file := func(path string) ConfigFile {
		return ConfigFile{Name: "app", Path: path, Profiles: pfProfiles(), Keys: keys}
	}
	const body = "region: eu\nprofiles:\n  prod:\n    region: us\n"
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", body)

	rtx := cwContext(dir, []string{"APP_PROFILE=prod"}, file(path))
	if err := SetConfigValue(rtx, "app", "db.host", "db.prod"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "ap", ToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "af", ToSharedKeys()); err != nil {
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
	if err := UnsetConfigValue(rtx, "app", "region", ToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); !strings.Contains(got, "  stage: {}\n") {
		t.Errorf("file:\n%s", got)
	}

	for _, opts := range [][]ConfigWriteOption{{ToProfile("a"), ToSharedKeys()}, {ToProfile("")}} {
		if err := SetConfigValue(rtx, "app", "region", "x", opts...); !errors.Is(err, ErrInternal) {
			t.Errorf("contradicting options: %v", err)
		}
	}
	plain := ConfigFile{Name: "app", Path: path, Keys: keys}
	if err := SetConfigValue(cwContext(dir, nil, plain), "app", "region", "x", ToProfile("a")); !errors.Is(err, ErrInternal) {
		t.Errorf("ToProfile on a file without profiles: %v", err)
	}
}

func TestSetConfigValue_schema(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.yaml", "region: eu\n")
	f := ConfigFile{
		Name: "app", Path: path, Keys: []ConfigKey{{Key: "region", Type: "string"}},
		Schema: `{"type":"object","properties":{"region":{"enum":["eu","us"]}}}`,
	}
	rtx := cwContext(dir, nil, f)
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	err := SetConfigValue(rtx, "app", "region", "ap")
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "config key region: configuration file "+path+" would be invalid:") {
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
	if err := SetConfigValue(rtx, "app", "owner", "ada"); !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "pass rotini.ParseConfigValue") {
		t.Errorf("a custom type without a parser: %v", err)
	}
	check := ParseConfigValue(func(raw string) error {
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
	keys := []ConfigKey{{Key: "a.b", Type: "int"}, {Key: "tags", Type: "[]string"}, {Key: "APP_NAME", Type: "string"}}
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
			f := ConfigFile{Name: "app", Path: path, As: tt.as, Keys: keys}
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
	f := ConfigFile{Name: "app", Path: path, Keys: []ConfigKey{{Key: "other.region", Type: "string"}}}
	err := SetConfigValue(cwContext(dir, nil, f), "app", "other.region", "us")
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "edit the file by hand") {
		t.Errorf("err = %v", err)
	}
	broken := pfWrite(t, dir, "broken.yaml", "region: [\n")
	f = ConfigFile{Name: "app", Path: broken, Keys: []ConfigKey{{Key: "region", Type: "string"}}}
	if err := SetConfigValue(cwContext(dir, nil, f), "app", "region", "us"); err == nil || !errors.Is(err, ErrUsage) {
		t.Errorf("an unparsable file: %v", err)
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
	f := ConfigFile{Name: "app", Path: path, Keys: []ConfigKey{
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
