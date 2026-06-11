package rotini

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeBinary writes an executable shell script to a fresh dir on PATH and
// returns the dir. On non-POSIX shells this would need adjusting, but the CI is
// unix.
func writeFakeBinary(t *testing.T, name, body string) {
	t.Helper()
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
	if code, _ := p.run(p.args); code != 0 {
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
	if code, _ := p.run(p.args); code != 3 {
		t.Errorf("remote exit code = %d, want 3", code)
	}
}

func TestRun_remoteNotFound(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{{Name: "missing", Binary: "app-no-such-plugin-xyz"}}}

	p, _, errb := remoteProgram(def, []string{"missing"})
	if code, _ := p.run(p.args); code != 1 {
		t.Errorf("missing remote exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
}

func TestRun_discoveryDispatch(t *testing.T) {
	writeFakeBinary(t, "acme-foo", "#!/bin/sh\necho \"plugin: $*\"\nexit 0\n")
	def := Definition{Name: "acme", Handler: "App", Discovery: &RemoteDiscoveryDef{Prefix: "acme-"}}

	// `acme foo x y` is not a declared command → discovery execs acme-foo with [x y].
	p, out, errb := remoteProgram(def, []string{"foo", "x", "y"})
	if code, _ := p.run(p.args); code != 0 {
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

	dir := t.TempDir()
	dirBin := filepath.Join(dir, name)
	if err := os.WriteFile(dirBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
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
	if code, _ := p.run(p.args); code != 1 {
		t.Errorf("missing discovered plugin exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
}
