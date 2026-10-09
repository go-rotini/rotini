package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestOSView_lookup(t *testing.T) {
	v := newOSView([]string{"A=1", "B=2", "A=3", "=C:=C:\\", "EMPTY=", "noequals"}, "", "linux")
	if got, ok := v.lookup("A"); !ok || got != "3" {
		t.Errorf("lookup(A) = %q, %v; want the later duplicate 3", got, ok)
	}
	if got, ok := v.lookup("EMPTY"); !ok || got != "" {
		t.Errorf("lookup(EMPTY) = %q, %v; want set and empty", got, ok)
	}
	if _, ok := v.lookup("a"); ok {
		t.Error("lookup(a) found A; names are case-sensitive outside Windows")
	}
	if _, ok := v.lookup("PATH"); ok {
		t.Error("lookup(PATH) found the process value; an injected environment is the whole environment")
	}
	if got, want := v.environ(), []string{"A=3", "B=2", "EMPTY="}; !slices.Equal(got, want) {
		t.Errorf("environ() = %q, want %q", got, want)
	}

	win := newOSView([]string{"Path=C:\\bin", "PATH=D:\\bin"}, "", "windows")
	if got, ok := win.lookup("path"); !ok || got != `D:\bin` {
		t.Errorf("windows lookup(path) = %q, %v; want the later spelling's value", got, ok)
	}
	if got := win.environ(); !slices.Equal(got, []string{`PATH=D:\bin`}) {
		t.Errorf("windows environ() = %q, want one entry", got)
	}

	empty := newOSView([]string{}, "", "linux")
	if !empty.injected() || len(empty.environ()) != 0 {
		t.Errorf("an empty slice is an empty environment: injected %v, environ %q", empty.injected(), empty.environ())
	}
	var process *osView
	if process.injected() {
		t.Error("the nil view reports injected")
	}
}

func TestOSView_home(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "USERPROFILE": `C:\Users\u`, "home": "/usr/u"}
	get := func(k string) string { return env[k] }
	for goos, want := range map[string]string{"linux": "/home/u", "darwin": "/home/u", "windows": `C:\Users\u`, "plan9": "/usr/u"} {
		if got, err := homeFrom(goos, get); err != nil || got != want {
			t.Errorf("homeFrom(%s) = %q, %v; want %q", goos, got, err, want)
		}
	}
	none := func(string) string { return "" }
	if _, err := homeFrom("linux", none); err == nil {
		t.Error("homeFrom with no HOME succeeded")
	}
	if got, _ := homeFrom("ios", none); got != "/" {
		t.Errorf("homeFrom(ios) = %q, want /", got)
	}
}

func TestOSView_xdgConfigDir(t *testing.T) {
	v := newOSView([]string{"HOME=/home/u"}, "", runtime.GOOS)
	if runtime.GOOS == "windows" {
		v = newOSView([]string{"USERPROFILE=/home/u"}, "", runtime.GOOS)
	}
	if got, err := v.xdgConfigDir("acme"); err != nil || got != filepath.Join("/home/u", ".config", "acme") {
		t.Errorf("xdgConfigDir = %q, %v; want home/.config/acme", got, err)
	}
	v = newOSView([]string{"XDG_CONFIG_HOME=/xdg"}, "", runtime.GOOS)
	if got, err := v.xdgConfigDir("acme"); err != nil || got != filepath.Join("/xdg", "acme") {
		t.Errorf("xdgConfigDir = %q, %v; want /xdg/acme", got, err)
	}
	if _, err := newOSView([]string{}, "", runtime.GOOS).xdgConfigDir("acme"); err == nil {
		t.Error("xdgConfigDir with no home succeeded")
	}
	if _, err := v.xdgConfigDir("../x"); err == nil {
		t.Error("xdgConfigDir accepted an app name with a separator")
	}
}

func TestOSView_expandPath(t *testing.T) {
	v := newOSView([]string{"HOME=/home/u", "USERPROFILE=/home/u", "SET=val", "EMPTY="}, "", runtime.GOOS)
	tests := []struct{ in, want string }{
		{"", ""},
		{"plain/path", "plain/path"},
		{"~", "/home/u"},
		{"~/cfg.yaml", "/home/u/cfg.yaml"},
		{"a/~/b", "a/~/b"},
		{"$SET/x", "val/x"},
		{"${SET}x", "valx"},
		{"$UNSET/x", "/x"},
		{"${UNSET-def}", "def"},
		{"${EMPTY-def}", ""},
		{"${EMPTY:-def}", "def"},
		{"${SET:-def}", "val"},
		{"${SET+alt}", "alt"},
		{"${UNSET+alt}", ""},
		{"${EMPTY:+alt}", ""},
		{"${SET:+alt}", "alt"},
		{"${SET:?msg}", "val"},
		{"$", "$"},
		{"a$1", "a$1"},
		{"${unclosed", "${unclosed"},
	}
	for _, tt := range tests {
		if got, err := v.expandPath(tt.in); err != nil || got != tt.want {
			t.Errorf("expandPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := v.expandPath("${UNSET:?set it}"); !errors.Is(err, errUnsetPathVariable) {
		t.Errorf("expandPath(${UNSET:?…}) error = %v, want the unset-variable error", err)
	}
	if _, err := newOSView([]string{}, "", runtime.GOOS).expandPath("~/x"); err == nil {
		t.Error("expandPath(~) with no home succeeded")
	}
}

func TestOSView_dir(t *testing.T) {
	dir := t.TempDir()
	v := (*osView)(nil).withDir(dir)
	if got := v.abs("a/b"); got != filepath.Join(dir, "a/b") {
		t.Errorf("abs(a/b) = %q", got)
	}
	abs := filepath.Join(dir, "x")
	if got := v.abs(abs); got != abs {
		t.Errorf("abs(%q) = %q, want it unchanged", abs, got)
	}
	if got := (*osView)(nil).abs("a"); got != "a" {
		t.Errorf("process abs(a) = %q, want it unchanged", got)
	}
	rel := (*osView)(nil).withDir("sub").resolved()
	if wd, _ := os.Getwd(); rel.base() != filepath.Join(wd, "sub") {
		t.Errorf("a relative dir resolved to %q, want it under the working directory", rel.base())
	}
}

func TestContext_environAccessors(t *testing.T) {
	dir := t.TempDir()
	rtx := NewContextFor(Definition{Name: "app"}, nil).WithEnviron([]string{"K=v"}).WithDir(dir)
	if got, ok := rtx.LookupEnv("K"); !ok || got != "v" {
		t.Errorf("LookupEnv(K) = %q, %v", got, ok)
	}
	if got := rtx.Environ(); !slices.Equal(got, []string{"K=v"}) {
		t.Errorf("Environ() = %q", got)
	}
	got := rtx.Environ()
	got[0] = "changed"
	if rtx.Environ()[0] != "K=v" {
		t.Error("Environ returned the view's own slice")
	}
	if rtx.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", rtx.Dir(), dir)
	}
	wd, _ := os.Getwd()
	if d := NewContextFor(Definition{Name: "app"}, nil).Dir(); d != wd {
		t.Errorf("Dir() with nothing injected = %q, want the working directory %q", d, wd)
	}
}

// injectedInputs reads env inputs (convention-named, explicit and listed), a flag's env
// fallback and a nested env family.
type injectedInputs struct {
	App struct {
		Flags struct {
			Port int `rotini:"port" recon:"server.port"`
		}
		Arguments struct{}
		Env       struct {
			Region string         `rotini:"region" recon:"region"`
			Token  string         `rotini:"token" recon:"token" env:"GH_TOKEN,GITHUB_TOKEN"`
			HTTP   map[string]any `rotini:"http" recon:"http" envnest:"ACME_HTTP,__"`
		}
	}
}

func injectedDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "port", Identifiers: []string{"--port"}, Type: "int"}}}
}

// TestInjectedEnv_bindsEveryEnvChannel pins that an injected environment feeds env inputs,
// listed variable names, env_prefix names, flag fallbacks and nested families, and shadows the
// process environment.
func TestInjectedEnv_bindsEveryEnvChannel(t *testing.T) {
	t.Setenv("REGION", "process")
	t.Setenv("ACME_REGION", "process")
	t.Setenv("GH_TOKEN", "process")
	env := []string{"ACME_REGION=eu", "GITHUB_TOKEN=tok", "ACME_SERVER_PORT=8080", "ACME_HTTP__RETRY__MAX=9"}

	rtx := NewContextFor(injectedDef(), nil).WithEnviron(env).WithInputSettings(InputSettings{EnvPrefix: "ACME"})
	in, err := rtx.Inputs[injectedInputs]()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	e := in.App.Env
	if e.Region != "eu" || e.Token != "tok" || in.App.Flags.Port != 8080 {
		t.Errorf("bound region=%q token=%q port=%d; want eu, tok, 8080 from the injected environment", e.Region, e.Token, in.App.Flags.Port)
	}
	if retry, _ := e.HTTP["retry"].(map[string]any); retry["max"] != "9" {
		t.Errorf("nested family = %v, want retry.max 9", e.HTTP)
	}

	layer, err := rtx.EnvInputs[injectedInputs]()
	if err != nil {
		t.Fatalf("EnvInputs: %v", err)
	}
	if layer.Values.App.Env.Region != "eu" || layer.Values.App.Flags.Port != 8080 {
		t.Errorf("EnvInputs bound %+v", layer.Values.App)
	}
}

// TestInjectedEnv_errorsNameTheInjectedVariable pins that a fallback error names the variable
// set in the run's environment, not the process's.
func TestInjectedEnv_errorsNameTheInjectedVariable(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "port", Identifiers: []string{"--port"}, Type: "int"}}}
	var in struct {
		App struct {
			Flags struct {
				Port int `rotini:"port" recon:"port" env:"APP_PORT,PORT"`
			}
		}
	}
	t.Setenv("APP_PORT", "1")
	rtx := NewContextFor(def, nil).WithEnviron([]string{"PORT=abc"})
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "environment variable PORT") {
		t.Fatalf("Read = %v, want the error to name PORT", err)
	}
}

// TestInjectedEnv_mapFlagIgnoresKeyVariables pins that a map flag's env fallback reads only its
// one declared variable: LABELS_K=v no longer adds a key.
func TestInjectedEnv_mapFlagIgnoresKeyVariables(t *testing.T) {
	rtx := NewContextFor(tbListDef(), nil).WithEnviron([]string{"LABELS_K=v", "LABELS_X=y"})
	var in tbListInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(in.App.Flags.Labels) != 0 {
		t.Errorf("labels = %v, want none from per-key variables", in.App.Flags.Labels)
	}
	rtx = NewContextFor(tbListDef(), nil).WithEnviron([]string{"LABELS=k=v"})
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if in.App.Flags.Labels["k"] != "v" {
		t.Errorf("labels = %v, want k=v from LABELS", in.App.Flags.Labels)
	}
}

// cfgInputs reads one config key.
type cfgInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Name string `rotini:"name" recon:"name"`
		}
	}
}

func readCfgName(t *testing.T, rtx *Context, files ...ConfigFile) string {
	t.Helper()
	var in cfgInputs
	if err := NewInputReader(InputSettings{ConfigFiles: files}).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	return in.App.Config.Name
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInjectedDir_configFiles pins that config discovery and config paths follow the run's
// directory and environment: walk-up, xdg, relative paths, $VAR and ~ in a path, and a
// config_source variable.
func TestInjectedDir_configFiles(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "proj", "sub")
	writeFile(t, filepath.Join(root, "proj", ".app.yaml"), "name: walk-up\n")
	writeFile(t, filepath.Join(home, ".config", "acme", "config.yaml"), "name: xdg-home\n")
	writeFile(t, filepath.Join(root, "xdg", "acme", "config.yaml"), "name: xdg\n")
	writeFile(t, filepath.Join(work, "local.yaml"), "name: relative\n")
	writeFile(t, filepath.Join(root, "vars", "app.yaml"), "name: var\n")
	writeFile(t, filepath.Join(home, "tilde.yaml"), "name: tilde\n")
	writeFile(t, filepath.Join(root, "override.yaml"), "name: override\n")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // the process value must not be read

	homeEnv := "HOME=" + home
	if runtime.GOOS == "windows" {
		homeEnv = "USERPROFILE=" + home
	}
	ctx := func(env ...string) *Context {
		return NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron(env).WithDir(work)
	}
	tests := []struct {
		name string
		rtx  *Context
		file ConfigFile
		want string
	}{
		{"walk-up from the run's directory", ctx(), ConfigFile{Name: "p", Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"}}, "walk-up"},
		{"xdg from the run's home", ctx(homeEnv), ConfigFile{Name: "u", Discover: &DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "acme"}}, "xdg-home"},
		{"xdg from the run's XDG_CONFIG_HOME", ctx("XDG_CONFIG_HOME=" + filepath.Join(root, "xdg")), ConfigFile{Name: "u", Discover: &DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "acme"}}, "xdg"},
		{"a relative path", ctx(), ConfigFile{Name: "l", Path: "local.yaml"}, "relative"},
		{"$VAR in a path", ctx("CFG_DIR=" + filepath.Join(root, "vars")), ConfigFile{Name: "v", Path: "$CFG_DIR/app.yaml"}, "var"},
		{"~ in a path", ctx(homeEnv), ConfigFile{Name: "t", Path: "~/tilde.yaml"}, "tilde"},
		{"config_source variable", ctx("APP_CONFIG=../../override.yaml"), ConfigFile{Name: "o", Path: "none.yaml", PathFrom: &PathFromDef{Env: "APP_CONFIG"}}, "override"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readCfgName(t, tt.rtx, tt.file); got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInjectedDir_argvPaths pins that a relative @file and an existingfile value resolve
// against the run's directory, and that errors name the path as typed.
func TestInjectedDir_argvPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "token.txt"), "s3cret\n")
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "token", Identifiers: []string{"--token"}, Type: "string", From: []string{"file"}},
		{Name: "manifest", Identifiers: []string{"--manifest"}, Type: "existingfile"},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Token    string `rotini:"token"`
				Manifest string `rotini:"manifest"`
			}
		}
	}
	var in inputs
	rtx := NewContextFor(def, []string{"--token", "@token.txt", "--manifest", "token.txt"}).WithDir(dir)
	if err := rtx.Parser().Parse(rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Token != "s3cret" || in.App.Flags.Manifest != "token.txt" {
		t.Errorf("bound %+v; want the file's contents and the path as typed", in.App.Flags)
	}

	rtx = NewContextFor(def, []string{"--token", "@missing.txt"}).WithDir(dir)
	if err := rtx.Parser().Parse(rtx, &in); err == nil || !strings.Contains(err.Error(), `"missing.txt"`) {
		t.Errorf("Parse(@missing.txt) = %v, want the path as typed", err)
	}
	rtx = NewContextFor(def, []string{"--manifest", "missing.txt"}).WithDir(dir)
	if err := rtx.Parser().Parse(rtx, &in); err == nil {
		t.Error("Parse(--manifest missing.txt) succeeded")
	}

	// CheckInputs checks a hand-built existingfile against the run's directory too.
	rtx = NewContextFor(def, nil).WithDir(dir)
	in.App.Flags.Manifest = "token.txt"
	if err := rtx.CheckInputs(in, Presence{"App.Flags.Manifest": {}}); err != nil {
		t.Errorf("CheckInputs = %v, want token.txt found in the run's directory", err)
	}
}

// TestInjectedEnv_plugins pins plugin lookup through the run's PATH and plugin path, and that
// the child gets exactly the run's environment and directory.
func TestInjectedEnv_plugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell-script stand-in for a plugin cannot run on Windows; see e2e r9_plugins")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"marker=$MARKER path=$PATH\"\npwd\n"
	writeExec(t, filepath.Join(bin, "app-env"), script)
	plugins := t.TempDir()
	writeExec(t, filepath.Join(plugins, "app-local"), script)
	t.Setenv("MARKER", "process")

	work := t.TempDir()
	run := func(def Definition, argv ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		p := NewProgram(def, &testHandlers{log: new([]string)}).
			WithEnviron([]string{"PATH=" + bin, "MARKER=injected", "PLUGINS=" + plugins}).
			WithDir(work).WithStdout(&out).WithStderr(&errb).WithoutSignalHandling()
		if code, _ := p.Run(argv); code != 0 {
			t.Fatalf("Run(%q) = %d, stderr %q", argv, code, errb.String())
		}
		return out.String()
	}
	wantDir, _ := filepath.EvalSymlinks(work)

	out := run(Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "env", Binary: "app-env"}}}, "env")
	if !strings.Contains(out, "marker=injected path="+bin+"\n") {
		t.Errorf("plugin saw %q, want the injected MARKER and PATH", out)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(strings.SplitN(out, "\n", 2)[1])); got != wantDir {
		t.Errorf("plugin ran in %q, want %q", got, wantDir)
	}

	out = run(Definition{Name: "app", Handler: "App", PluginPath: "$PLUGINS", Plugins: []PluginDef{{Name: "local", Binary: "app-local"}}}, "local")
	if !strings.Contains(out, "marker=injected") {
		t.Errorf("plugin_path plugin saw %q", out)
	}

	disc := Definition{Name: "app", Handler: "App", PluginPath: "$PLUGINS", PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"}}
	rtx := NewContextFor(disc, nil).WithEnviron([]string{"PATH=" + bin, "PLUGINS=" + plugins})
	cmd := rtx.Command()
	var names []string
	for _, p := range cmd.DiscoveredPlugins() {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"env", "local"}) {
		t.Errorf("DiscoveredPlugins = %q, want env from the run's PATH and local from its plugin path", names)
	}
	if path, ok := cmd.PluginBinary("env"); !ok || path != filepath.Join(bin, "app-env") {
		t.Errorf("PluginBinary(env) = %q, %v", path, ok)
	}
	if got := complete(disc, []string{""}, nil, NewContextFor(disc, nil).WithEnviron([]string{"PATH=" + bin})); !slices.Contains(got, "env") {
		t.Errorf("completion offered %q, want env from the run's PATH", got)
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestInjectedEnv_completion pins that the messages switch and a completer read the run's
// environment.
func TestInjectedEnv_completion(t *testing.T) {
	t.Setenv("APP_MESSAGES", "on")
	h := &msgHandlers{add: []string{"from the completer"}}
	p := NewProgram(msgDef("APP_MESSAGES"), h).WithEnviron([]string{"APP_MESSAGES=off"})
	if got := messagesOf(t, p, "deploy", ""); got != nil {
		t.Errorf("Messages = %q, want none: the injected APP_MESSAGES=off hides them", got)
	}

	envH := &envCompleteHandlers{}
	p = NewProgram(msgDef(""), envH).WithEnviron([]string{"SERVICES=api,web"})
	var got CompletionResult
	if _, err := p.Complete([]string{"deploy", ""}, func(_ io.Writer, r CompletionResult) error { got = r; return nil }); err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, c := range got.Candidates {
		values = append(values, c.Value)
	}
	if !slices.Equal(values, []string{"api", "web"}) {
		t.Errorf("candidates = %q, want the injected SERVICES", values)
	}
}

type envCompleteHandlers struct{}

func (*envCompleteHandlers) AppDeploy() Handler { return envDeploy{} }

type envDeploy struct{ NoHooks }

func (envDeploy) Run(context.Context, *Context) {}
func (envDeploy) CompleteArgValue(rtx *Context, _, _ string) []string {
	v, _ := rtx.LookupEnv("SERVICES")
	return strings.Split(v, ",")
}

// TestInjectedEnv_parallelPrograms runs programs with different environments and directories
// at once; under -race it proves the runs share nothing.
func TestInjectedEnv_parallelPrograms(t *testing.T) {
	type in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Who string `rotini:"who" recon:"who"`
			}
			Config struct {
				Name string `rotini:"name" recon:"name"`
			}
		}
	}
	var mu sync.Mutex
	seen := map[string]string{}
	h := &funcHandlers{run: func(rtx *Context) {
		v, err := rtx.Inputs[in]()
		if err != nil {
			rtx.HaltWith(err)
			return
		}
		mu.Lock()
		seen[v.App.Env.Who] = v.App.Config.Name + "@" + filepath.Base(rtx.Dir())
		mu.Unlock()
	}}
	def := Definition{Name: "app", Handler: "App"}
	settings := InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Path: "app.yaml"}}}
	for i := range 20 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), fmt.Sprintf("d%d", i))
			writeFile(t, filepath.Join(dir, "app.yaml"), fmt.Sprintf("name: n%d\n", i))
			p := NewProgram(def, h).WithInputSettings(settings).
				WithEnviron([]string{fmt.Sprintf("WHO=w%d", i)}).WithDir(dir).
				WithStdout(io.Discard).WithStderr(io.Discard).WithoutSignalHandling()
			if code, err := p.Run(nil); code != 0 {
				t.Fatalf("Run = %d, %v", code, err)
			}
		})
	}
	t.Cleanup(func() {
		for i := range 20 {
			if got, want := seen[fmt.Sprintf("w%d", i)], fmt.Sprintf("n%d@d%d", i, i); got != want {
				t.Errorf("run %d read %q, want %q", i, got, want)
			}
		}
	})
}

// funcHandlers runs one function as the root's Run.
type funcHandlers struct{ run func(*Context) }

func (h *funcHandlers) App() Handler { return funcHandler{run: h.run} }

type funcHandler struct {
	NoHooks
	run func(*Context)
}

func (f funcHandler) Run(_ context.Context, rtx *Context) { f.run(rtx) }
