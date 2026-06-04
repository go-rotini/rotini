package rtk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionInstallTarget(t *testing.T) {
	cfg := t.TempDir()
	data := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", data)

	cases := []struct {
		shell    string
		wantPath string
		wantCmd  []string // substrings that must appear in Command
		wantNote string   // a substring the Note must contain
	}{
		{
			"bash", filepath.Join(data, "bash-completion", "completions", "mycli"),
			[]string{"mkdir -p", "mycli completion bash"}, "bash-completion",
		},
		{
			"zsh", filepath.Join(data, "zsh", "site-functions", "_mycli"),
			[]string{"mycli completion zsh"}, "fpath",
		},
		{
			"fish", filepath.Join(cfg, "fish", "completions", "mycli.fish"),
			[]string{"mycli completion fish"}, "fish",
		},
		{
			"powershell", "$PROFILE",
			[]string{"mycli completion powershell", "$PROFILE"}, "PowerShell",
		},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			got, err := CompletionInstallTarget("mycli", c.shell)
			if err != nil {
				t.Fatalf("CompletionInstallTarget: %v", err)
			}
			if got.Shell != c.shell {
				t.Errorf("Shell = %q, want %q", got.Shell, c.shell)
			}
			if got.Path != c.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, c.wantPath)
			}
			for _, sub := range c.wantCmd {
				if !strings.Contains(got.Command, sub) {
					t.Errorf("Command %q missing %q", got.Command, sub)
				}
			}
			if got.Note == "" || !strings.Contains(got.Note, c.wantNote) {
				t.Errorf("Note = %q, want it to contain %q", got.Note, c.wantNote)
			}
		})
	}
}

// Unsupported / empty shells error with the same vocabulary as CompletionScript.
func TestCompletionInstallTarget_errors(t *testing.T) {
	if _, err := CompletionInstallTarget("mycli", ""); err == nil {
		t.Error("empty shell should error")
	}
	if _, err := CompletionInstallTarget("mycli", "nushell"); err == nil {
		t.Error("unsupported shell should error")
	}
}

// A spaced install dir is single-quoted so the install command is copy-paste safe.
func TestCompletionInstallTarget_quotesSpaces(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/with space")
	got, err := CompletionInstallTarget("mycli", "zsh")
	if err != nil {
		t.Fatalf("CompletionInstallTarget: %v", err)
	}
	if !strings.Contains(got.Command, `'/tmp/with space/zsh/site-functions/_mycli'`) {
		t.Errorf("Command should single-quote the spaced path: %q", got.Command)
	}
}

// With no XDG override, paths fall back under the user's home (~/.config, ~/.local/share)
// on every platform — not the OS-native config dir.
func TestCompletionInstallTarget_homeFallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home) // honored by os.UserHomeDir on unix

	got, err := CompletionInstallTarget("mycli", "fish")
	if err != nil {
		t.Fatalf("CompletionInstallTarget: %v", err)
	}
	want := filepath.Join(home, ".config", "fish", "completions", "mycli.fish")
	if got.Path != want {
		t.Errorf("Path = %q, want %q (XDG-style home fallback)", got.Path, want)
	}
}

// InstallCompletion writes the script atomically to the resolved path, creating the
// directory, and returns where it landed; re-installing overwrites it.
func TestInstallCompletion(t *testing.T) {
	cfg := t.TempDir()
	data := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", data)

	cases := []struct {
		shell string
		want  string
	}{
		{"bash", filepath.Join(data, "bash-completion", "completions", "mycli")},
		{"zsh", filepath.Join(data, "zsh", "site-functions", "_mycli")},
		{"fish", filepath.Join(cfg, "fish", "completions", "mycli.fish")},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			script := "# " + c.shell + " completion for mycli\n"
			path, err := InstallCompletion("mycli", c.shell, script)
			if err != nil {
				t.Fatalf("InstallCompletion: %v", err)
			}
			if path != c.want {
				t.Errorf("path = %q, want %q", path, c.want)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read written file: %v", err)
			}
			if string(got) != script {
				t.Errorf("written content = %q, want %q", got, script)
			}

			// Re-install with new content overwrites in place.
			updated := script + "# updated\n"
			if _, err := InstallCompletion("mycli", c.shell, updated); err != nil {
				t.Fatalf("re-install: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != updated {
				t.Errorf("re-install did not overwrite: %q", got)
			}
		})
	}
}

// powershell cannot be auto-installed ($PROFILE is shell-resolved); the error points at
// the manual command. Unsupported / empty shells error too.
func TestInstallCompletion_errors(t *testing.T) {
	if _, err := InstallCompletion("mycli", "powershell", "x"); err == nil ||
		!strings.Contains(err.Error(), "$PROFILE") || !strings.Contains(err.Error(), "mycli completion powershell") {
		t.Errorf("powershell install err = %v, want it to explain $PROFILE + the manual command", err)
	}
	if _, err := InstallCompletion("mycli", "nushell", "x"); err == nil {
		t.Error("unsupported shell should error")
	}
	if _, err := InstallCompletion("mycli", "", "x"); err == nil {
		t.Error("empty shell should error")
	}
}
