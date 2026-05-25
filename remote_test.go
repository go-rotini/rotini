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
	p := NewProgram(def, &testHandlers{log: new([]string)}).WithArguments(args)
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
	if code := p.run(p.args); code != 0 {
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
	if code := p.run(p.args); code != 3 {
		t.Errorf("remote exit code = %d, want 3", code)
	}
}

func TestRun_remoteNotFound(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{{Name: "missing", Binary: "app-no-such-plugin-xyz"}}}

	p, _, errb := remoteProgram(def, []string{"missing"})
	if code := p.run(p.args); code != 1 {
		t.Errorf("missing remote exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", errb)
	}
}
