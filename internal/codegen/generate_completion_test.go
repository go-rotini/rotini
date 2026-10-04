package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the shell idioms that pass every token, including an empty current word, to
// the binary. They check the idiom rather than behavior because most shells are absent on CI;
// bash is exercised for real in e2e/testdata/script/r2_completion_shells.txtar.

// TestCompletionScripts_preserveEveryToken pins the token-assembly line of each shell script.
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

// TestCompletionScript_bashSlicesBeforeNarrowingIFS pins that the bash script slices COMP_WORDS
// before narrowing IFS: bash 3.2 (macOS /bin/bash) joins "${array[@]:offset:length}" into one
// element when IFS lacks a space, so every word after the first would be lost.
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
// syntax checker, skipping shells that are not installed.
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
