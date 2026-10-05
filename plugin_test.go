package rotini

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeFakeBinary writes an executable shell script to a fresh dir on PATH. Tests built on it
// skip on Windows; the e2e scripts cover plugin dispatch there with real binaries.
func writeFakeBinary(t *testing.T, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell-script stand-in for a plugin cannot run on Windows; see e2e r9_plugins")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func pluginProgram(def Definition, args []string) (*Program, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgram(def, &testHandlers{log: new([]string)}).WithArgs(args)
	p.stdout, p.stderr = out, errb
	return p, out, errb
}

func TestRun_pluginExecPassesThrough(t *testing.T) {
	writeFakeBinary(t, "app-ext", "#!/bin/sh\necho \"ext ran: $*\"\nexit 0\n")
	def := Definition{
		Name:    "app",
		Handler: "App",
		Plugins: []PluginDef{{Name: "ext", Aliases: []string{"x"}, Binary: "app-ext"}},
	}

	p, out, errb := pluginProgram(def, []string{"ext", "hello", "world"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "ext ran: hello world" {
		t.Errorf("plugin output = %q", got)
	}
}

func TestRun_pluginPropagatesExitCode(t *testing.T) {
	writeFakeBinary(t, "app-fail", "#!/bin/sh\nexit 3\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "fail", Binary: "app-fail"}}}

	p, _, _ := pluginProgram(def, []string{"fail"})
	if code, _ := p.Run(p.args); code != 3 {
		t.Errorf("plugin exit code = %d, want 3", code)
	}
}

func TestRun_pluginNotFound(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{{Name: "missing", Binary: "app-no-such-plugin-xyz"}}}

	p, _, errb := pluginProgram(def, []string{"missing"})
	code, err := p.Run(p.args)
	// A declared plugin's missing binary is internal; the default reporter exits 1.
	if code != 1 {
		t.Errorf("missing plugin exit = %d, want %d", code, 1)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
	if CategoryOf(err) != CategoryInternal {
		t.Errorf("CategoryOf = %v, want internal", CategoryOf(err))
	}
	var re *PluginError
	if !errors.As(err, &re) || re.Kind != PluginNotFound {
		t.Errorf("err = %v, want a *PluginError of kind binary-not-found", err)
	} else if re.Name != "missing" {
		t.Errorf("PluginError.Name = %q, want missing", re.Name)
	}
}

func TestRun_discoveryDispatch(t *testing.T) {
	writeFakeBinary(t, "acme-foo", "#!/bin/sh\necho \"plugin: $*\"\nexit 0\n")
	def := Definition{Name: "acme", Handler: "App", PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}}

	// `acme foo x y` is not a declared command → discovery execs acme-foo with [x y].
	p, out, errb := pluginProgram(def, []string{"foo", "x", "y"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "plugin: x y" {
		t.Errorf("discovered plugin output = %q, want %q", got, "plugin: x y")
	}
}

// TestResolvePluginBinary_resolutionOrder pins plugin path before PATH, the fall-through to
// PATH, and the not-found error. The next-to-executable step cannot be seeded, so the plugin
// names never exist beside the test binary.
func TestResolvePluginBinary_resolutionOrder(t *testing.T) {
	const name = "rotini-resolveorder-plugin-xyz"
	// The file a program named `name` lives in: Windows finds programs by extension.
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}

	dir := t.TempDir()
	dirBin := filepath.Join(dir, file)
	if err := os.WriteFile(dirBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, file), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	// The plugin path wins over a same-named binary on PATH.
	if got, err := resolvePluginBinary(name, dir); err != nil {
		t.Fatalf("resolve via dir: %v", err)
	} else if got != dirBin {
		t.Errorf("resolved %q, want the discovery-path copy %q (dir beats PATH)", got, dirBin)
	}

	// dir misses → fall through to PATH.
	if got, err := resolvePluginBinary(name, t.TempDir()); err != nil {
		t.Fatalf("resolve via PATH: %v", err)
	} else if filepath.Dir(got) != pathDir {
		t.Errorf("resolved %q, want the PATH copy under %q", got, pathDir)
	}

	_, err := resolvePluginBinary("rotini-absent-plugin-zzz", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("absent binary: err = %v, want a 'not found' error", err)
	}
}

func TestRun_discoveryMissing(t *testing.T) {
	def := Definition{Name: "acme", Handler: "App", PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}}

	p, _, errb := pluginProgram(def, []string{"no-such-plugin-xyz"})
	code, err := p.Run(p.args)
	// A discovered token with no binary is a usage error; the default reporter exits 1.
	if code != 1 {
		t.Errorf("missing discovered plugin exit = %d, want %d", code, 1)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}
	var re *PluginError
	if !errors.As(err, &re) || re.Kind != PluginNotFound {
		t.Errorf("err = %v, want a *PluginError of kind binary-not-found", err)
	}
}

// TestRun_pluginTimeout pins that a plugin past its timeout is killed and reported as a
// CategoryNone *PluginError of kind timeout, exiting 1.
func TestRun_pluginTimeout(t *testing.T) {
	writeFakeBinary(t, "app-slow", "#!/bin/sh\nsleep 5\n")
	def := Definition{Name: "app", Handler: "App", Plugins: []PluginDef{
		{Name: "slow", Binary: "app-slow", Timeout: 50 * time.Millisecond},
	}}

	p, _, errb := pluginProgram(def, []string{"slow"})
	code, err := p.Run(p.args)
	var re *PluginError
	if !errors.As(err, &re) || re.Kind != PluginTimeout {
		t.Fatalf("err = %v, want a *PluginError of kind timeout", err)
	}
	if re.Timeout != 50*time.Millisecond {
		t.Errorf("PluginError.Timeout = %s, want 50ms", re.Timeout)
	}
	if CategoryOf(err) != CategoryNone {
		t.Errorf("CategoryOf = %v, want none (a timeout is neither party's fault)", CategoryOf(err))
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (CategoryNone floors to 1)", code)
	}
	if !strings.Contains(errb.String(), "timed out") {
		t.Errorf("stderr = %q, want 'timed out'", errb)
	}
}

// TestPluginBinary pins that Command.PluginBinary resolves declared plugins, aliases and
// discovered tokens through the same plugin path dispatch uses.
func TestPluginBinary(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, progFile(name))
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The discovery path holds both; neither sits next to the test binary or on PATH.
	write("app-found")
	write("app-only")

	cmd := Command{
		Name: "app",
		Plugins: []PluginDef{
			{Name: "found", Binary: "app-found", Aliases: []string{"f"}},
			{Name: "gone", Binary: "app-gone-nothing-here"},
		},
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"}, PluginPath: dir,
	}

	t.Run("a declared plugin sees the plugin path", func(t *testing.T) {
		p, ok := cmd.PluginBinary("found")
		if !ok {
			t.Fatal("app-found is in the plugin path and did not resolve")
		}
		if filepath.Dir(p) != dir {
			t.Errorf("resolved to %q, want it under %q", p, dir)
		}
	})
	t.Run("a discovered token sees the same directory", func(t *testing.T) {
		p, ok := cmd.PluginBinary("only")
		if !ok {
			t.Fatal("app-only is in the plugin path and did not resolve")
		}
		if filepath.Dir(p) != dir {
			t.Errorf("resolved to %q, want it under %q", p, dir)
		}
	})
	t.Run("with no plugin path, neither kind sees it", func(t *testing.T) {
		bare := cmd
		bare.PluginPath = ""
		if p, ok := bare.PluginBinary("found"); ok {
			t.Errorf("declared plugin resolved to %q with no PluginPath set", p)
		}
		if p, ok := bare.PluginBinary("only"); ok {
			t.Errorf("discovered token resolved to %q with no PluginPath set", p)
		}
	})
	t.Run("a declared plugin with no binary anywhere", func(t *testing.T) {
		if _, ok := cmd.PluginBinary("gone"); ok {
			t.Error("a plugin with no binary reported as resolvable")
		}
	})
	t.Run("an unknown name with no discovery", func(t *testing.T) {
		if _, ok := (Command{Name: "app"}).PluginBinary("whatever"); ok {
			t.Error("a command with no plugins and no discovery resolved something")
		}
	})
	t.Run("an alias answers identically to its plugin", func(t *testing.T) {
		byName, okName := cmd.PluginBinary("found")
		byAlias, okAlias := cmd.PluginBinary("f")
		if byName != byAlias || okName != okAlias {
			t.Errorf("alias gave (%q, %t), name gave (%q, %t)", byAlias, okAlias, byName, okName)
		}
	})
}

// TestRun_pluginHonorsProgramStdin pins that a plugin reads the Program's stdin, not the
// process's.
func TestRun_pluginHonorsProgramStdin(t *testing.T) {
	writeFakeBinary(t, "app-cat", "#!/bin/sh\nwc -l\n")
	def := Definition{
		Name:    "app",
		Handler: "App",
		Plugins: []PluginDef{{Name: "cat", Binary: "app-cat"}},
	}

	p, out, errb := pluginProgram(def, []string{"cat"})
	p.stdin = strings.NewReader("alpha\nbeta\ngamma\n")

	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "3" {
		t.Errorf("the plugin read %q lines from stdin, want 3 — the Program's stdin was not passed through", got)
	}
}

func TestPluginErrorKind_String(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []PluginErrorKind{PluginNotFound, PluginTimeout, PluginStartFailed} {
		s := k.String()
		if s == "" {
			t.Errorf("kind %d renders empty", k)
		}
		if seen[s] {
			t.Errorf("kind %d renders %q, already used by another kind", k, s)
		}
		seen[s] = true
	}
}

// TestExecutableNames_windowsRules pins the Windows executable-extension rules (host-sync found
// as host-sync.exe, offered as "sync") on every OS.
func TestExecutableNames_windowsRules(t *testing.T) {
	win := executableExts("windows", "")
	if want := []string{".com", ".exe", ".bat", ".cmd"}; !slices.Equal(win, want) {
		t.Fatalf("default PATHEXT = %q, want %q", win, want)
	}
	if got := executableExts("windows", ".EXE; .Cmd"); !slices.Equal(got, []string{".exe", ".cmd"}) {
		t.Errorf("PATHEXT is lower-cased and trimmed: got %q", got)
	}
	if got := executableExts("linux", ".exe"); got != nil {
		t.Errorf("only Windows has executable extensions: got %q", got)
	}

	if got := executableFileNames("host-sync", win); !slices.Contains(got, "host-sync.exe") || slices.Contains(got, "host-sync") {
		t.Errorf("windows lookup for host-sync = %q, want host-sync.exe and not the bare name", got)
	}
	if got := executableFileNames("host-sync.exe", win); got[0] != "host-sync.exe" {
		t.Errorf("a name already carrying an extension is tried as given first: got %q", got)
	}
	if got := executableFileNames("host-sync", nil); !slices.Equal(got, []string{"host-sync"}) {
		t.Errorf("elsewhere the file is taken as named: got %q", got)
	}

	for _, tc := range []struct {
		file, want string
		ok         bool
	}{
		{"host-sync.exe", "host-sync", true},
		{"host-sync.EXE", "host-sync", true},
		{"host-sync.cmd", "host-sync", true},
		{"host-sync", "host-sync", false},
		{"host-sync.txt", "host-sync.txt", false},
	} {
		if got, ok := trimExecutableExt(tc.file, win); got != tc.want || ok != tc.ok {
			t.Errorf("trimExecutableExt(%q) = (%q, %v), want (%q, %v)", tc.file, got, ok, tc.want, tc.ok)
		}
	}
	if got, ok := trimExecutableExt("host-sync", nil); got != "host-sync" || !ok {
		t.Errorf("elsewhere every file counts, unchanged: got (%q, %v)", got, ok)
	}
}
