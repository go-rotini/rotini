package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// ── @file and "-" on list and map flags ──────────────────────────────────────

type idListFlags struct {
	Tags   []string          `rotini:"tags"`
	Paths  []string          `rotini:"paths"`
	Labels map[string]string `rotini:"labels"`
	Spec   string            `rotini:"spec"`
}
type idListCmd struct {
	Flags     idListFlags
	Arguments struct{}
}
type idListInputs struct{ App idListCmd }

var idListDef = Definition{Name: "app", Handler: "App", Flags: []FlagDef{
	{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Separator: ",", From: []string{"file", "stdin"}},
	{Name: "paths", Identifiers: []string{"--paths"}, Type: "[]string", Separator: "\x00", From: []string{"file", "stdin"}},
	{Name: "labels", Identifiers: []string{"--labels"}, Type: "map[string]string", From: []string{"file"}},
	{Name: "spec", Identifiers: []string{"--spec"}, Type: "string", From: []string{"file"}},
}}

func idBind(t *testing.T, dir, stdin string, argv ...string) (idListInputs, error) {
	t.Helper()
	rtx := NewContextFor(idListDef, argv).WithDir(dir).WithStdin(strings.NewReader(stdin))
	var in idListInputs
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	return in, err
}

func idWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAcquiredListFlag_oneValuePerLine(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want []string
	}{
		"lines":                  {"t1\nt2\n", []string{"t1", "t2"}},
		"crlf":                   {"t1\r\nt2\r\n", []string{"t1", "t2"}},
		"blank lines skipped":    {"t1\n\n  \nt2", []string{"t1", "t2"}},
		"bom":                    {"\ufefft1\nt2\n", []string{"t1", "t2"}},
		"split on the separator": {"a,b\nc,d\n", []string{"a", "b", "c", "d"}},
		"hash is a value":        {"#red\n", []string{"#red"}},
		"empty file":             {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			idWrite(t, dir, "tags.txt", tc.file)
			in, err := idBind(t, dir, "", "--tags", "@tags.txt")
			if err != nil || !slices.Equal(in.App.Flags.Tags, tc.want) {
				t.Errorf("tags = %q, %v; want %q", in.App.Flags.Tags, err, tc.want)
			}
		})
	}
}

func TestAcquiredListFlag_stdinAndMaps(t *testing.T) {
	dir := t.TempDir()
	in, err := idBind(t, dir, "x\ny,z\n", "--tags", "-")
	if err != nil || !slices.Equal(in.App.Flags.Tags, []string{"x", "y", "z"}) {
		t.Errorf("--tags - = %q, %v", in.App.Flags.Tags, err)
	}

	idWrite(t, dir, "labels.txt", "k=v\nx=y\n")
	in, err = idBind(t, dir, "", "--labels", "@labels.txt")
	if want := map[string]string{"k": "v", "x": "y"}; err != nil || !reflect.DeepEqual(in.App.Flags.Labels, want) {
		t.Errorf("labels = %v, %v; want %v", in.App.Flags.Labels, err, want)
	}

	// A literal value is unchanged, and so is a doubled @, and a single-value flag's file.
	in, err = idBind(t, dir, "", "--tags", "@@x,y", "--spec", "@labels.txt")
	if err != nil || !slices.Equal(in.App.Flags.Tags, []string{"@x", "y"}) || in.App.Flags.Spec != "k=v\nx=y" {
		t.Errorf("literal tags = %q, spec = %q, %v", in.App.Flags.Tags, in.App.Flags.Spec, err)
	}
}

func TestAcquiredListFlag_nul(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, "list", "a b\x00c\nd\x00\r\x00")
	in, err := idBind(t, dir, "", "--paths", "@list")
	if want := []string{"a b", "c\nd", "\r"}; err != nil || !slices.Equal(in.App.Flags.Paths, want) {
		t.Errorf("paths = %q, %v; want %q", in.App.Flags.Paths, err, want)
	}
	in, err = idBind(t, dir, "x\x00y\x00", "--paths", "-")
	if err != nil || !slices.Equal(in.App.Flags.Paths, []string{"x", "y"}) {
		t.Errorf("--paths - = %q, %v", in.App.Flags.Paths, err)
	}
	in, err = idBind(t, dir, "", "--paths", "lit,eral")
	if err != nil || !slices.Equal(in.App.Flags.Paths, []string{"lit,eral"}) {
		t.Errorf("a literal with a NUL separator = %q, %v", in.App.Flags.Paths, err)
	}
}

func TestAcquiredLines_comments(t *testing.T) {
	text := "\ufeffa\n# note\n  #x\n\nb\r\n"
	if got := acquiredLines(text, false, true); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("with comments = %q", got)
	}
	if got := acquiredLines(text, false, false); !slices.Equal(got, []string{"a", "# note", "  #x", "b"}) {
		t.Errorf("without comments = %q", got)
	}
}

// ── variable_file ────────────────────────────────────────────────────────────

type idSecretCmd struct {
	Flags struct {
		Token string `rotini:"token" recon:"token" env:"APP_TOKEN" envfile:"APP_TOKEN_FILE"`
	}
	Arguments struct{}
	Env       struct {
		Password string `rotini:"password" recon:"password,secret" env:"APP_PASSWORD,DB_PASSWORD" envfile:"APP_PASSWORD_FILE"`
	}
}
type idSecretInputs struct{ App idSecretCmd }

var idSecretDef = Definition{Name: "app", Handler: "App", Flags: []FlagDef{
	{Name: "token", Identifiers: []string{"--token"}, Type: "string"},
	{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
}}

func idSecretBind(t *testing.T, dir string, env []string, argv ...string) (idSecretInputs, error) {
	t.Helper()
	rtx := NewContextFor(idSecretDef, argv).WithDir(dir).WithEnviron(env)
	var in idSecretInputs
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	return in, err
}

func TestVariableFile(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, "token", "s3cret\n")
	idWrite(t, dir, "pw", "hunter2\r\n")

	in, err := idSecretBind(t, dir, []string{"APP_TOKEN_FILE=token", "APP_PASSWORD_FILE=" + filepath.Join(dir, "pw")})
	if err != nil || in.App.Flags.Token != "s3cret" || in.App.Env.Password != "hunter2" {
		t.Fatalf("from files: token %q, password %q, %v", in.App.Flags.Token, in.App.Env.Password, err)
	}

	in, err = idSecretBind(t, dir, []string{"APP_TOKEN=plain", "APP_TOKEN_FILE="})
	if err != nil || in.App.Flags.Token != "plain" {
		t.Errorf("an empty file variable is unset: token %q, %v", in.App.Flags.Token, err)
	}

	in, err = idSecretBind(t, dir, []string{"APP_TOKEN_FILE=token"}, "--token", "argv")
	if err != nil || in.App.Flags.Token != "argv" {
		t.Errorf("argv wins over the file: token %q, %v", in.App.Flags.Token, err)
	}

	_, err = idSecretBind(t, dir, []string{"APP_TOKEN=plain", "APP_TOKEN_FILE=token"})
	if err == nil || err.Error() != "set APP_TOKEN or APP_TOKEN_FILE, not both" || !errors.Is(err, ErrUsage) {
		t.Errorf("both set = %v", err)
	}
	_, err = idSecretBind(t, dir, []string{"DB_PASSWORD=x", "APP_PASSWORD_FILE=pw"})
	if err == nil || err.Error() != "set DB_PASSWORD or APP_PASSWORD_FILE, not both" {
		t.Errorf("any of the names set = %v", err)
	}

	_, err = idSecretBind(t, dir, []string{"APP_TOKEN_FILE=missing"})
	if err == nil || err.Error() != `APP_TOKEN_FILE: no such file: "missing"` {
		t.Errorf("unreadable = %v", err)
	}
	if _, err := idSecretBind(t, dir, []string{"APP_TOKEN_FILE=missing"}, "--help"); err != nil {
		t.Errorf("--help with an unreadable file = %v, want it skipped", err)
	}

	big := strings.Repeat("x", envFileCap+1)
	idWrite(t, dir, "big", big)
	if _, err := idSecretBind(t, dir, []string{"APP_TOKEN_FILE=big"}); err == nil || !strings.Contains(err.Error(), "larger than 1 MiB") {
		t.Errorf("an oversized file = %v", err)
	}
}

type idSecretMinCmd struct {
	Flags     struct{}
	Arguments struct{}
	Env       struct {
		Password string `rotini:"password" recon:"password,secret" env:"APP_PASSWORD" envfile:"APP_PASSWORD_FILE" minlen:"10"`
	}
}
type idSecretMinInputs struct{ App idSecretMinCmd }

// A secret read from a file is redacted, and its error names the file variable.
func TestVariableFile_secretRedacted(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, "pw", "short")
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir).WithEnviron([]string{"APP_PASSWORD_FILE=pw"})
	var in idSecretMinInputs
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	if err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("err = %v, want a redacted constraint error", err)
	}
	if !strings.Contains(err.Error(), "(from the file named by APP_PASSWORD_FILE)") {
		t.Errorf("err = %v, want it to name the file variable", err)
	}
}

// A value read through a file names the file variable as its origin.
func TestVariableFile_origin(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, "token", "s3cret\n")
	idWrite(t, dir, "pw", "hunter2\n")
	rtx := NewContextFor(idSecretDef, nil).WithDir(dir).
		WithEnviron([]string{"APP_TOKEN_FILE=token", "APP_PASSWORD_FILE=pw"})
	layer, err := rtx.EnvInputs[idSecretInputs]()
	must(t, err)
	for path, want := range map[FieldPath]string{
		"App.Flags.Token":  "env:APP_TOKEN_FILE",
		"App.Env.Password": "env:APP_PASSWORD_FILE",
	} {
		if got := layer.Set[path].Origin; got != want {
			t.Errorf("origin of %s = %q, want %q", path, got, want)
		}
	}

	rtx = NewContextFor(idSecretDef, nil).WithDir(dir).WithEnviron([]string{"DB_PASSWORD=x"})
	layer, err = rtx.EnvInputs[idSecretInputs]()
	must(t, err)
	if got := layer.Set["App.Env.Password"].Origin; got != "env:DB_PASSWORD" {
		t.Errorf("origin of a plain variable = %q, want env:DB_PASSWORD", got)
	}
}

// ── a .env file as an environment layer ─────────────────────────────────────

type idEnvCmd struct {
	Flags struct {
		Region string `rotini:"region" recon:"region" env:"APP_REGION"`
	}
	Arguments struct{}
	Env       struct {
		Endpoint string         `rotini:"endpoint" recon:"endpoint" env:"APP_ENDPOINT"`
		Token    string         `rotini:"token" recon:"token,secret" env:"APP_TOKEN" envfile:"APP_TOKEN_FILE"`
		Labels   map[string]any `rotini:"labels" recon:"labels" envnest:"APP_LABEL,__"`
	}
	Config struct {
		Endpoint string `rotini:"cfg-endpoint" recon:"APP_ENDPOINT"`
	}
}
type idEnvInputs struct{ App idEnvCmd }

func idEnvMeta(dir string) InputSettings {
	return InputSettings{ConfigFiles: []ConfigFile{
		{Name: "dotenv", Scope: "app", Path: filepath.Join(dir, ".env"), Format: "dotenv", As: "env"},
		{Name: "base", Scope: "app", Path: filepath.Join(dir, "base.env"), As: "env"},
		{Name: "conf", Scope: "app", Path: filepath.Join(dir, "conf.yaml"), Format: "yaml"},
	}}
}

func idEnvBind(t *testing.T, dir string, env []string, argv ...string) (idEnvInputs, error) {
	t.Helper()
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "region", Identifiers: []string{"--region"}, Type: "string", Default: "def"},
		{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
	}}
	rtx := NewContextFor(def, argv).WithDir(dir).WithEnviron(env)
	var in idEnvInputs
	err := NewInputReader(idEnvMeta(dir)).Read(rtx, &in)
	return in, err
}

func TestDotenvLayer_precedence(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, ".env", "APP_ENDPOINT=from-dotenv\nAPP_REGION=eu\nAPP_LABEL__TEAM=core\nAPP_TOKEN_FILE=tok\nHOME=/nowhere\n")
	idWrite(t, dir, "base.env", "APP_ENDPOINT=from-base\nAPP_LABEL__TIER=1\n")
	idWrite(t, dir, "conf.yaml", "region: from-config\nAPP_ENDPOINT: from-config\n")
	idWrite(t, dir, "tok", "t0k\n")

	in, err := idEnvBind(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Env.Endpoint != "from-dotenv" || in.App.Flags.Region != "eu" || in.App.Env.Token != "t0k" {
		t.Errorf(".env over the next .env and config: %+v", in.App)
	}
	if want := map[string]any{"team": "core", "tier": "1"}; !reflect.DeepEqual(in.App.Env.Labels, want) {
		t.Errorf("labels = %v, want %v from both files", in.App.Env.Labels, want)
	}
	if in.App.Config.Endpoint != "from-config" {
		t.Errorf("a .env file is not configuration: config endpoint = %q", in.App.Config.Endpoint)
	}

	in, err = idEnvBind(t, dir, []string{"APP_ENDPOINT=real", "APP_REGION=us", "APP_TOKEN=plain"})
	if err != nil || in.App.Env.Endpoint != "real" || in.App.Flags.Region != "us" || in.App.Env.Token != "plain" {
		t.Errorf("the real environment wins: %+v, %v", in.App, err)
	}

	in, err = idEnvBind(t, dir, nil, "--region", "argv")
	if err != nil || in.App.Flags.Region != "argv" {
		t.Errorf("argv wins: region %q, %v", in.App.Flags.Region, err)
	}
}

func TestDotenvLayer_errors(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, ".env", "APP_ENDPOINT=x\nAPP_TOKEN=a\nAPP_TOKEN_FILE=b\n")
	if _, err := idEnvBind(t, dir, nil); err == nil || err.Error() != "set APP_TOKEN or APP_TOKEN_FILE, not both" {
		t.Errorf("a pair within one .env = %v", err)
	}
	if _, err := idEnvBind(t, dir, []string{"APP_TOKEN=real"}); err != nil {
		t.Errorf("the real environment decides the pair: %v", err)
	}

	idWrite(t, dir, ".env", "this is not dotenv ===\n\"unterminated\n")
	if _, err := idEnvBind(t, dir, nil); err == nil {
		t.Error("a malformed .env should fail")
	}
	if _, err := idEnvBind(t, dir, nil, "--help"); err != nil {
		t.Errorf("--help with a malformed .env = %v, want it skipped", err)
	}
}

func TestDotenvLayer_origin(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, ".env", "APP_REGION=\n")
	type cmd struct {
		Flags struct {
			Count int `rotini:"count" recon:"count" env:"APP_COUNT"`
		}
		Arguments struct{}
	}
	type inputs struct{ App cmd }
	idWrite(t, dir, ".env", "APP_COUNT=many\n")
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int"}}}
	rtx := NewContextFor(def, nil).WithDir(dir).WithEnviron([]string{})
	var in inputs
	err := NewInputReader(idEnvMeta(dir)).Read(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "environment variable APP_COUNT (from .env)") {
		t.Errorf("err = %v, want the value's origin named", err)
	}
}

// ── native and xdg-system discovery ──────────────────────────────────────────

func TestUserConfigDirFor(t *testing.T) {
	env := map[string]string{"AppData": `C:\Users\me\AppData\Roaming`, "XDG_CONFIG_HOME": "/xdg", "home": "/usr/me"}
	getenv := func(k string) string { return env[k] }
	home := func() (string, error) { return "/home/me", nil }
	for goos, want := range map[string]string{
		"windows": `C:\Users\me\AppData\Roaming`,
		"darwin":  filepath.Join("/home/me", "Library", "Application Support"),
		"ios":     filepath.Join("/home/me", "Library", "Application Support"),
		"plan9":   filepath.Join("/usr/me", "lib"),
		"linux":   "/xdg",
	} {
		if got, err := userConfigDirFor(goos, getenv, home); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", goos, got, err, want)
		}
	}
	env["XDG_CONFIG_HOME"] = "relative"
	if got, _ := userConfigDirFor("linux", getenv, home); got != filepath.Join("/home/me", ".config") {
		t.Errorf("a relative XDG_CONFIG_HOME is ignored: %q", got)
	}
	delete(env, "AppData")
	if _, err := userConfigDirFor("windows", getenv, home); err == nil {
		t.Error("windows without %AppData% should fail")
	}
}

func TestXDGConfigDirsFor(t *testing.T) {
	sep := string(os.PathListSeparator)
	if got := xdgConfigDirsFor("linux", ""); !slices.Equal(got, []string{"/etc/xdg"}) {
		t.Errorf("default = %q", got)
	}
	if got := xdgConfigDirsFor("windows", ""); got != nil {
		t.Errorf("windows default = %q, want none", got)
	}
	abs := filepath.Join(t.TempDir(), "a")
	if got := xdgConfigDirsFor("linux", abs+sep+"rel"+sep+sep+"/b"); runtime.GOOS != "windows" && !slices.Equal(got, []string{abs, "/b"}) {
		t.Errorf("listed = %q, relative and empty entries skipped", got)
	}
}

// The user's native file and the system tier layer per key: user over system.
func TestDiscover_nativeOverSystem(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	user, err := userConfigDirFor(runtime.GOOS, func(k string) string {
		return map[string]string{"AppData": filepath.Join(home, "AppData"), "XDG_CONFIG_HOME": filepath.Join(home, "cfg")}[k]
	}, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	sys1, sys2 := filepath.Join(root, "sys1"), filepath.Join(root, "sys2")
	for path, body := range map[string]string{
		filepath.Join(user, "demo", "config.yaml"): "region: user\n",
		filepath.Join(sys2, "demo", "config.yaml"): "region: system\nendpoint: sys2\n",
		filepath.Join(sys1, "demo", "config.yaml"): "endpoint: sys1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		idWrite(t, filepath.Dir(path), filepath.Base(path), body)
	}
	type cmd struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Region   string `rotini:"region" recon:"region"`
			Endpoint string `rotini:"endpoint" recon:"endpoint"`
		}
	}
	type inputs struct{ App cmd }
	meta := InputSettings{ConfigFiles: []ConfigFile{
		{Name: "user", Scope: "app", Format: "yaml", Discover: &DiscoverDef{Strategy: "native", App: "demo", File: "config.yaml"}},
		{Name: "system", Scope: "app", Format: "yaml", Discover: &DiscoverDef{Strategy: "xdg-system", App: "demo", File: "config.yaml"}},
	}}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "AppData=" + filepath.Join(home, "AppData"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "cfg"), "XDG_CONFIG_DIRS=" + sys1 + string(os.PathListSeparator) + sys2}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron(env)
	var in inputs
	if err := NewInputReader(meta).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.App.Config.Region != "user" || in.App.Config.Endpoint != "sys1" {
		t.Errorf("config = %+v, want region from the user file, endpoint from the first system directory", in.App.Config)
	}
}

// With no system directory to search (Windows without XDG_CONFIG_DIRS), the file is absent.
func TestDiscover_systemNone(t *testing.T) {
	dirs, err := discoverSystemDirs(&DiscoverDef{Strategy: "xdg-system", App: "demo", File: "c.yaml"}, newOSView([]string{"XDG_CONFIG_DIRS=rel"}, "", runtime.GOOS))
	if err != nil || len(dirs) != 0 {
		t.Errorf("dirs = %q, %v; want none", dirs, err)
	}
	b := NewInputReader(InputSettings{})
	src, _, err := b.openFileSource(ConfigFile{Name: "s", Format: "yaml", Discover: &DiscoverDef{Strategy: "xdg-system", App: "demo", File: "c.yaml"}}, nil, newOSView([]string{"XDG_CONFIG_DIRS=rel"}, t.TempDir(), runtime.GOOS))
	if err != nil || len(src.Keys()) != 0 {
		t.Errorf("source = %v, %v; want an empty one", src, err)
	}
}
