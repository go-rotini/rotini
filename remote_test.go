package rotini

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// OnError exits 1 (EH3 maps category → code).
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
	code, err := p.Run(p.args)
	// A DISCOVERED token resolving to no binary is the user's typo (usage), so
	// the default OnError exits 1 (EH3 maps category → code).
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
// (operational). It is still a recorded error, so the default OnError exits 1.
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
// NEXT TO THE HOST BINARY first (the git/kubectl convention), then in the discovery path for a
// discovered plugin, then on PATH. A doctor built on LookPath reports every conventionally
// installed plugin as missing, which is what example-plug's `plugins` command did before this.
func TestRemoteBinaryPath(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
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
		Discovery: &RemoteDiscoveryDef{Prefix: "app-", Path: dir},
	}

	t.Run("a declared remote does NOT see the discovery path", func(t *testing.T) {
		// app-found exists only in the discovery path, and a declared remote is not
		// searched there — dispatch would fail too, so the doctor must agree.
		if p, ok := RemoteBinaryPath(cmd, "found"); ok {
			t.Errorf("declared remote resolved to %q via the discovery path", p)
		}
	})
	t.Run("a discovered token does", func(t *testing.T) {
		p, ok := RemoteBinaryPath(cmd, "only")
		if !ok {
			t.Fatal("app-only is in the discovery path and did not resolve")
		}
		if filepath.Dir(p) != dir {
			t.Errorf("resolved to %q, want it under %q", p, dir)
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
		// "f" is an alias of "found". Both take the DECLARED path, so neither sees the
		// discovery path — an alias that resolved differently from its own remote would
		// make the doctor disagree with dispatch.
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
