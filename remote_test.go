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

// writeFakeBinary writes an executable shell script to a fresh dir on PATH. Windows cannot run
// a shell script as a program, so tests built on one skip there; plugin dispatch on Windows is
// covered by the e2e r9 scripts, which build and run real plugin binaries.
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

func remoteProgram(def Definition, args []string) (*Program, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgram(def, &testHandlers{log: new([]string)}).WithArgs(args)
	p.stdout, p.stderr = out, errb
	return p, out, errb
}

func TestRun_remoteExecPassesThrough(t *testing.T) {
	writeFakeBinary(t, "app-ext", "#!/bin/sh\necho \"ext ran: $*\"\nexit 0\n")
	def := Definition{
		Name:           "app",
		Handler:        "App",
		RemoteCommands: []RemoteDef{{Name: "ext", Aliases: []string{"x"}, Binary: "app-ext"}},
	}

	p, out, errb := remoteProgram(def, []string{"ext", "hello", "world"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "ext ran: hello world" {
		t.Errorf("remote output = %q", got)
	}
}

func TestRun_remotePropagatesExitCode(t *testing.T) {
	writeFakeBinary(t, "app-fail", "#!/bin/sh\nexit 3\n")
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{{Name: "fail", Binary: "app-fail"}}}

	p, _, _ := remoteProgram(def, []string{"fail"})
	if code, _ := p.Run(p.args); code != 3 {
		t.Errorf("remote exit code = %d, want 3", code)
	}
}

func TestRun_remoteNotFound(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{{Name: "missing", Binary: "app-no-such-plugin-xyz"}}}

	p, _, errb := remoteProgram(def, []string{"missing"})
	code, err := p.Run(p.args)
	// A DECLARED remote whose binary is missing is internal, so the default
	// funnel exits 1 (it maps no category to a code).
	if code != 1 {
		t.Errorf("missing remote exit = %d, want %d", code, 1)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
	// A DECLARED remote whose binary is missing is an install/wiring problem.
	if CategoryOf(err) != CategoryInternal {
		t.Errorf("CategoryOf = %v, want internal", CategoryOf(err))
	}
	// EH6: a typed, As-able *RemoteError naming the kind.
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteBinaryNotFound {
		t.Errorf("err = %v, want a *RemoteError of kind binary-not-found", err)
	} else if re.Name != "missing" {
		t.Errorf("RemoteError.Name = %q, want missing", re.Name)
	}
}

func TestRun_discoveryDispatch(t *testing.T) {
	writeFakeBinary(t, "acme-foo", "#!/bin/sh\necho \"plugin: $*\"\nexit 0\n")
	def := Definition{Name: "acme", Handler: "App", Discovery: &RemoteDiscoveryDef{Prefix: "acme-"}}

	// `acme foo x y` is not a declared command → discovery execs acme-foo with [x y].
	p, out, errb := remoteProgram(def, []string{"foo", "x", "y"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "plugin: x y" {
		t.Errorf("discovered plugin output = %q, want %q", got, "plugin: x y")
	}
}

// resolveRemoteBinary searches adjacent-to-executable → discovery dir → PATH, in that
// order. This pins the dir > PATH precedence, the fall-through to PATH when dir misses,
// and the not-found error naming all three locations. (The adjacent step uses the live
// os.Executable() dir, which a test cannot seed, so the plugin name is one that never
// sits beside the test binary — exercising the dir/PATH tail.)
func TestResolveRemoteBinary_resolutionOrder(t *testing.T) {
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

	// dir (discovery path) wins over a same-named binary on PATH.
	if got, err := resolveRemoteBinary(name, dir); err != nil {
		t.Fatalf("resolve via dir: %v", err)
	} else if got != dirBin {
		t.Errorf("resolved %q, want the discovery-path copy %q (dir beats PATH)", got, dirBin)
	}

	// dir misses → fall through to PATH.
	if got, err := resolveRemoteBinary(name, t.TempDir()); err != nil {
		t.Fatalf("resolve via PATH: %v", err)
	} else if filepath.Dir(got) != pathDir {
		t.Errorf("resolved %q, want the PATH copy under %q", got, pathDir)
	}

	// Nowhere → an error naming every search location.
	_, err := resolveRemoteBinary("rotini-absent-plugin-zzz", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("absent binary: err = %v, want a 'not found' error", err)
	}
}

func TestRun_discoveryMissing(t *testing.T) {
	def := Definition{Name: "acme", Handler: "App", Discovery: &RemoteDiscoveryDef{Prefix: "acme-"}}

	p, _, errb := remoteProgram(def, []string{"no-such-plugin-xyz"})
	code, err := p.Run(p.args)
	// A DISCOVERED token resolving to no binary is the user's typo (usage), so
	// the default funnel exits 1 (it maps no category to a code).
	if code != 1 {
		t.Errorf("missing discovered plugin exit = %d, want %d", code, 1)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
	// A DISCOVERED token that resolves to no binary is the user's typo.
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}
	// EH6: same typed *RemoteError, but usage-categorized for a discovered miss.
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteBinaryNotFound {
		t.Errorf("err = %v, want a *RemoteError of kind binary-not-found", err)
	}
}

// TestRun_remoteTimeout: a plugin that runs past its declared timeout is killed
// and surfaced as a *RemoteError of kind timeout — deliberately CategoryNone
// (operational). It is still a recorded error, so the default funnel exits 1.
func TestRun_remoteTimeout(t *testing.T) {
	writeFakeBinary(t, "app-slow", "#!/bin/sh\nsleep 5\n")
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
		{Name: "slow", Binary: "app-slow", Timeout: 50 * time.Millisecond},
	}}

	p, _, errb := remoteProgram(def, []string{"slow"})
	code, err := p.Run(p.args)
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteTimeout {
		t.Fatalf("err = %v, want a *RemoteError of kind timeout", err)
	}
	if re.Timeout != 50*time.Millisecond {
		t.Errorf("RemoteError.Timeout = %s, want 50ms", re.Timeout)
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

// TestRemoteBinaryPath covers the doctor seam: asking, from outside, whether a remote
// sub-command would actually resolve.
//
// It exists because the obvious way to answer that — exec.LookPath — is wrong. Dispatch looks
// NEXT TO THE HOST BINARY first (the git/kubectl convention), then in the command's
// PluginPath, then on PATH. A doctor built on LookPath reports every conventionally installed
// plugin as missing, which is what example-plug's `plugins` command did before this.
//
// The PluginPath half is the same for both kinds of remote, and deliberately so: the path
// used to live on RemoteDiscovery, where only DISCOVERED plugins could reach it, so an author
// had no way to say where a DECLARED remote lived. They are the same binaries in the same
// directory; configuring that twice, or only for one of them, was the accident.
func TestRemoteBinaryPath(t *testing.T) {
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

	cmd := ResolvedCommand{
		Name: "app",
		Remotes: []RemoteDef{
			{Name: "found", Binary: "app-found", Aliases: []string{"f"}},
			{Name: "gone", Binary: "app-gone-nothing-here"},
		},
		Discovery: &RemoteDiscoveryDef{Prefix: "app-"}, PluginPath: dir,
	}

	t.Run("a declared remote sees the plugin path", func(t *testing.T) {
		p, ok := RemoteBinaryPath(cmd, "found")
		if !ok {
			t.Fatal("app-found is in the plugin path and did not resolve")
		}
		if filepath.Dir(p) != dir {
			t.Errorf("resolved to %q, want it under %q", p, dir)
		}
	})
	t.Run("a discovered token sees the same directory", func(t *testing.T) {
		p, ok := RemoteBinaryPath(cmd, "only")
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
		if p, ok := RemoteBinaryPath(bare, "found"); ok {
			t.Errorf("declared remote resolved to %q with no PluginPath set", p)
		}
		if p, ok := RemoteBinaryPath(bare, "only"); ok {
			t.Errorf("discovered token resolved to %q with no PluginPath set", p)
		}
	})
	t.Run("a declared remote with no binary anywhere", func(t *testing.T) {
		if _, ok := RemoteBinaryPath(cmd, "gone"); ok {
			t.Error("a remote with no binary reported as resolvable")
		}
	})
	t.Run("an unknown name with no discovery", func(t *testing.T) {
		if _, ok := RemoteBinaryPath(ResolvedCommand{Name: "app"}, "whatever"); ok {
			t.Error("a command with no remotes and no discovery resolved something")
		}
	})
	t.Run("an alias answers identically to its remote", func(t *testing.T) {
		// "f" is an alias of "found": an alias that resolved differently from its own
		// remote would make the doctor disagree with dispatch.
		byName, okName := RemoteBinaryPath(cmd, "found")
		byAlias, okAlias := RemoteBinaryPath(cmd, "f")
		if byName != byAlias || okName != okAlias {
			t.Errorf("alias gave (%q, %t), name gave (%q, %t)", byAlias, okAlias, byName, okName)
		}
	})
}

// TestRun_remoteHonorsProgramStdin covers the third stream.
//
// execRemote wired the child's stdout and stderr from the Program — honoring
// [Program.WithStdout] and [Program.WithStderr] — and then set cmd.Stdin = os.Stdin, reaching
// around [Program.WithStdin] to the process. Two streams redirected and one not is invisible
// until something actually feeds a plugin: a test, a REPL wrapping one, a host embedding the
// CLI. example-plug's suite could dispatch to a real plugin and could not give it input.
//
// The default is unaffected — p.stdin IS os.Stdin unless replaced — so an interactive plugin
// still receives the terminal, which exec.Cmd passes through as a raw fd for an *os.File.
func TestRun_remoteHonorsProgramStdin(t *testing.T) {
	writeFakeBinary(t, "app-cat", "#!/bin/sh\nwc -l\n")
	def := Definition{
		Name:           "app",
		Handler:        "App",
		RemoteCommands: []RemoteDef{{Name: "cat", Binary: "app-cat"}},
	}

	p, out, errb := remoteProgram(def, []string{"cat"})
	p.stdin = strings.NewReader("alpha\nbeta\ngamma\n")

	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errb)
	}
	if got := strings.TrimSpace(out.String()); got != "3" {
		t.Errorf("the plugin read %q lines from stdin, want 3 — the Program's stdin was not passed through", got)
	}
}

func TestRemoteErrorKind_String(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []RemoteErrorKind{RemoteBinaryNotFound, RemoteTimeout, RemoteSpawnFailed} {
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

// On Windows a program is a file with an executable extension: plugin lookup must find
// host-sync as host-sync.exe, and discovery must offer host-sync.exe as "sync". These run the
// Windows rules on any OS, so a macOS or Linux run catches a regression too.
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
