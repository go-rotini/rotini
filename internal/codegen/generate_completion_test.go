package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The completion scripts are generated SHELL, and Go's tooling sees them as opaque strings.
// Nothing in the codegen suite executes them, so a script can be wrong in a way that compiles,
// renders, round-trips through every golden file — and offers the user nothing. Three such bugs
// shipped at once, one per shell, all of the same shape: the tokens the shell hands the binary
// were silently truncated, so completion worked for the first word and failed everywhere else.
//
// These tests pin the exact idioms that fix them. They are written against the idiom rather
// than the behavior because two of the three shells are not installed on most CI runners; the
// bash one, which is, is exercised for real in e2e/testdata/script/r2_completion_shells.txtar.

// TestCompletionScripts_preserveEveryToken guards the token-assembly line of each shell script.
func TestCompletionScripts_preserveEveryToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		shell string
		want  string
		why   string
	}{
		{
			shell: "bash",
			want:  `args=("${COMP_WORDS[@]:1:$COMP_CWORD}")`,
			why:   "the slice must happen before IFS is narrowed (see the reject below)",
		},
		{
			shell: "zsh",
			want:  `"${(@)words[2,$CURRENT]}"`,
			why:   "an unquoted zsh slice drops the empty current word",
		},
		{
			shell: "fish",
			want:  `set -a tokens "$cur"`,
			why:   "an unquoted (commandline -ct) yields no element when the token is empty",
		},
		{
			shell: "powershell",
			want:  `$tokens += $(if ($legacy) { '""' } else { '' })`,
			why:   "the empty current word must reach the binary, and Windows PowerShell 5.1 drops a bare '' argument",
		},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			t.Parallel()
			script, err := completionScript("prog", tt.shell)
			if err != nil {
				t.Fatalf("completionScript(%s): %v", tt.shell, err)
			}
			if !strings.Contains(script, tt.want) {
				t.Errorf("the %s script no longer contains %s\nwhy it matters: %s", tt.shell, tt.want, tt.why)
			}
		})
	}
}

// TestCompletionScript_bashSlicesBeforeNarrowingIFS is the specific reject for the bash bug.
//
// bash 3.2 — /bin/bash on every macOS — collapses "${array[@]:offset:length}" into a single
// IFS-joined element whenever IFS does not contain a space. The script declared IFS=$'\n' on
// its `local` line, one line above the slice, so `prog hash --algorithm <TAB>` sent the binary
// ONE argument, "hash --algorithm ", which matches no command. Top-level completion still
// worked (a one-element slice survives being joined), which is why it looked fine.
func TestCompletionScript_bashSlicesBeforeNarrowingIFS(t *testing.T) {
	t.Parallel()
	script, err := completionScript("prog", "bash")
	if err != nil {
		t.Fatal(err)
	}
	slice := strings.Index(script, `args=("${COMP_WORDS[@]:1:$COMP_CWORD}")`)
	ifs := strings.Index(script, `local IFS=$'\n'`)
	switch {
	case slice < 0:
		t.Fatal("the COMP_WORDS slice is gone")
	case ifs < 0:
		t.Fatal("IFS is no longer narrowed at all — candidates containing spaces will split")
	case ifs < slice:
		t.Error("IFS is narrowed BEFORE the COMP_WORDS slice; bash 3.2 collapses the slice to one element")
	}
}

// TestCompletionScripts_parseInTheirOwnShell runs each generated script through its shell's
// syntax checker, for the shells this machine actually has. A generated script that does not
// parse is the one failure mode a user cannot work around, and it is invisible to `go test`.
func TestCompletionScripts_parseInTheirOwnShell(t *testing.T) {
	t.Parallel()
	checks := []struct {
		shell string
		bin   string
		args  []string // the syntax-check invocation; the script path is appended
	}{
		{"bash", "bash", []string{"-n"}},
		{"zsh", "zsh", []string{"-n"}},
		{"fish", "fish", []string{"--no-execute"}},
	}
	for _, c := range checks {
		t.Run(c.shell, func(t *testing.T) {
			t.Parallel()
			bin, err := exec.LookPath(c.bin)
			if err != nil {
				t.Skipf("%s is not installed", c.bin)
			}
			script, err := completionScript("prog", c.shell)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "completion."+c.shell)
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bin, append(c.args, path)...).CombinedOutput()
			if err != nil {
				t.Errorf("the generated %s script does not parse: %v\n%s", c.shell, err, out)
			}
		})
	}
}
