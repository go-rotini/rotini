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
			want:  `args+=("$REPLY")`,
			why:   "each word, the empty current one included, is passed as one argument once unquoted",
		},
		{
			shell: "zsh",
			want:  `args+=("${(Q)words[CURRENT]}")`,
			why:   "the current word, even empty, must be passed as one argument",
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
		{
			shell: "nushell",
			want:  `let words = ($place.command | skip 1)`,
			why:   "Nushell 0.116 passes the words with the partial one last (an empty string after a space) in place.command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			t.Parallel()
			script, err := completionScript("prog", tt.shell, completionEnvs{})
			if err != nil {
				t.Fatalf("completionScript(%s): %v", tt.shell, err)
			}
			if !strings.Contains(script, tt.want) {
				t.Errorf("the %s script no longer contains %s\nwhy it matters: %s", tt.shell, tt.want, tt.why)
			}
		})
	}
}

// TestCompletionScript_bashSlicesBeforeNarrowingIFS pins that the bash script gathers the words
// before narrowing IFS: bash 3.2 (macOS /bin/bash) joins "${array[@]:offset:length}" into one
// element when IFS lacks a space, so every word after the first would be lost.
func TestCompletionScript_bashSlicesBeforeNarrowingIFS(t *testing.T) {
	t.Parallel()
	script, err := completionScript("prog", "bash", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	slice := strings.Index(script, "\n    _prog_words\n")
	ifs := strings.Index(script, `local IFS=$'\n'`)
	switch {
	case slice < 0:
		t.Fatal("the words are no longer gathered in _prog_words")
	case ifs < 0:
		t.Fatal("IFS is no longer narrowed at all — candidates containing spaces will split")
	case ifs < slice:
		t.Error("IFS is narrowed BEFORE the words are gathered; bash 3.2 collapses an array slice to one element")
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
		{"nushell", "nu", []string{"--no-config-file"}},
	}
	for _, c := range checks {
		t.Run(c.shell, func(t *testing.T) {
			t.Parallel()
			bin, err := exec.LookPath(c.bin)
			if err != nil {
				t.Skipf("%s is not installed", c.bin)
			}
			// A name with "-" can't appear in shell variable names.
			for _, prog := range []string{"prog", "kubectl-prog"} {
				script, err := completionScript(prog, c.shell, completionEnvs{})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "completion."+c.shell)
				if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command(bin, append(c.args, path)...).CombinedOutput()
				if err != nil {
					t.Errorf("the generated %s script for %s does not parse: %v\n%s", c.shell, prog, err, out)
				}
			}
		})
	}
}

// TestCompletionScripts_readTheProtocol pins what each script reads of the binary's answer: the
// option line before the kind line, and an arm for every shell-completed kind.
func TestCompletionScripts_readTheProtocol(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"bash": {
			`":rotini:option "*)`, `_PROG_compopt -o nospace`, `_PROG_compopt -o nosort`,
			`compgen -c --`, `compgen -u --`, `compgen -g --`, `compgen -A hostname --`,
			`[[ -z $1 || $1 == file* ]]`, `[[ $COMP_TYPE == 63 ]]`, `_PROG_describe`,
		},
		"zsh": {
			`':rotini:option '*`, `opts+=(-S '')`, `_describe -V 'PROG' pairs`,
			`_command_names -e`, `_users`, `_groups`, `_hosts`, `"") (( $#pairs )) || _files ;;`,
		},
		"fish": {
			`':rotini:option *'`, `complete -c PROG -f -k -a '(__PROG_complete)'`,
			`__fish_complete_command`, `__fish_complete_users`, `__fish_complete_groups`, `__fish_print_hostnames`,
		},
		"powershell": {
			`$_ -notlike ':rotini:option *'`, `Get-Command -CommandType Application`, `Get-LocalUser`, `Get-LocalGroup`, `'host' { $names = @() }`,
		},
		"nushell": {
			`str starts-with ':rotini:option '`, `append_whitespace: $space`, `options: {sort: (not ('keep-order' in $options))}`,
			`commandline complete --type directory`, `commandline complete --type path`, `@complete "nu-complete PROG"`,
			`extern PROG [...args: string]`,
		},
	}
	for shell, idioms := range want {
		script, err := completionScript("PROG", shell, completionEnvs{})
		if err != nil {
			t.Fatal(err)
		}
		for _, idiom := range idioms {
			if !strings.Contains(script, idiom) {
				t.Errorf("the %s script no longer contains %s", shell, idiom)
			}
		}
		// The option arm must come before the generic directive arm, or it is read as a kind.
		if o, d := strings.Index(script, "rotini:option "), strings.LastIndex(script, `':rotini:'*`+"\n"); shell == "zsh" && (o < 0 || d >= 0 && d < o) {
			t.Errorf("zsh reads the option line after the directive")
		}
	}
}

// completionDriveDir returns a directory holding a fake "my-prog" that answers __complete with
// lines, and the files ab, aZ and secret.txt for a glob to match.
func completionDriveDir(t *testing.T, lines string) string {
	t.Helper()
	dir := t.TempDir()
	fake := "#!/bin/sh\nprintf -- '" + lines + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "my-prog"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ab", "aZ", "secret.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCompletionScript_bashDrivesDashedNameAndKeepsCandidatesLiteral runs the bash script for a
// program named with "-" against a fake binary: candidates are neither glob-expanded against the
// working directory nor split, and a lone candidate is quoted for insertion.
func TestCompletionScript_bashDrivesDashedNameAndKeepsCandidatesLiteral(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := completionScript("my-prog", "bash", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		lines string
		kind  string // COMP_TYPE: 63 lists the candidates as they are, 9 inserts them
		want  string
	}{
		{lines: "*\\na?\\n[ab]\\nsp ace\\n:rotini:none\\n", kind: "63", want: "<*>\n<a?>\n<[ab]>\n<sp ace>\n"},
		{lines: "sp ace\\n:rotini:none\\n", kind: "9", want: "<sp\\ ace>\n"},
	} {
		dir := completionDriveDir(t, tt.lines)
		path := filepath.Join(dir, "completion.bash")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		drive := `source "$1"; COMP_WORDS=(my-prog --scope ""); COMP_LINE="my-prog --scope "; COMP_CWORD=2; COMP_TYPE=$2; _my_2dprog_complete; printf '<%s>\n' "${COMPREPLY[@]}"`
		cmd := exec.Command(bash, "-c", drive, "bash", path, tt.kind)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash: %v\n%s", err, out)
		}
		if string(out) != tt.want {
			t.Errorf("COMPREPLY for %q =\n%s\nwant\n%s", tt.lines, out, tt.want)
		}
	}
}

// TestCompletionScript_fishDrivesDashedName runs fish's own completion for a program named with
// "-" against a fake binary.
func TestCompletionScript_fishDrivesDashedName(t *testing.T) {
	t.Parallel()
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	script, err := completionScript("my-prog", "fish", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	dir := completionDriveDir(t, "--alpha\\tfirst\\n--beta\\n:rotini:none\\n")
	path := filepath.Join(dir, "completion.fish")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(fish, "--no-config", "-c", "source $argv[1]; complete -C 'my-prog -'", path)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish: %v\n%s", err, out)
	}
	if want := "--alpha\tfirst\n--beta\n"; string(out) != want {
		t.Errorf("fish completions = %q, want %q", out, want)
	}
}

// TestCompletionScript_headerNamesTheSwitches pins the header lines for both switch variables.
func TestCompletionScript_headerNamesTheSwitches(t *testing.T) {
	t.Parallel()
	script, err := completionScript("app", "bash", completionEnvs{messages: "APP_MESSAGES", descriptions: "APP_DESCRIPTIONS"})
	if err != nil {
		t.Fatal(err)
	}
	want := "# bash completion for app\n# Set APP_MESSAGES=off to hide completion messages.\n# Set APP_DESCRIPTIONS=off to hide completion descriptions.\n"
	if !strings.HasPrefix(script, want) {
		t.Errorf("header = %q, want %q", script[:len(want)], want)
	}
}

// TestCompletionDescriptionsEnv pins where descriptions_env reaches: the runtime literal, the
// root man page, the contract and the scripts, and nothing without the completion feature.
func TestCompletionDescriptionsEnv(t *testing.T) {
	gp := messagesProgram(t, "", "")
	gp.conf.Generate.Features[0].DescriptionsEnv = "APP_DESCRIPTIONS"
	if got := completionDescriptionsLiteral(gp.conf); got != `CompletionDescriptions: &rotini.CompletionDescriptionsDef{Env: "APP_DESCRIPTIONS"},`+"\n" {
		t.Errorf("literal = %q", got)
	}
	root := flattenFeature(gp, manFeatureDesc)[0].data
	if len(root.Environment) == 0 || root.Environment[len(root.Environment)-1].Var != "APP_DESCRIPTIONS" {
		t.Errorf("root man ENVIRONMENT = %+v, want APP_DESCRIPTIONS listed", root.Environment)
	}
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"descriptions_env": "APP_DESCRIPTIONS"`) || strings.Contains(string(doc), `"messages_env"`) {
		t.Errorf("contract completion block wrong:\n%s", doc)
	}
	gp.conf.Generate.Features[0].Enabled = false
	if got := completionScriptEnvs(gp.conf); got != (completionEnvs{}) {
		t.Errorf("feature off: %+v, want none", got)
	}
}
