package rtk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
)

// CompletionTarget describes where a shell's completion script conventionally installs,
// as pure data — rotini writes nothing. A `completion install` handler can print the
// Command for the user to run, show the Path, or perform the write itself; the choice
// (and every filesystem change) stays with the handler and the end-user (Pillar 1).
type CompletionTarget struct {
	Shell   string // the shell this targets, as passed in
	Path    string // the conventional destination for the script ("$PROFILE" for powershell)
	Command string // a copy-paste shell command that installs the script at Path
	Note    string // a setup caveat the path alone cannot satisfy ("" when none)
}

// CompletionInstallTarget resolves where prog's completion script conventionally installs
// for shell and returns it as data — it computes the path but writes NOTHING. prog is the
// installed binary name (the command that emits the script, e.g. a `completion <shell>`
// command backed by [CompletionScript]). Supported shells match [CompletionScript]: bash,
// zsh, fish, powershell.
//
// Paths use the XDG base directories on every platform, because the shells read the XDG
// dirs even on macOS — so the result is where the shell actually looks for completions,
// not the OS-native config dir. The Note flags setup a path alone cannot guarantee: a zsh
// directory must be on $fpath, bash needs the bash-completion runtime, and a new shell
// must be started.
//
// It is the data-first companion to [CompletionScript]: generate the script with one,
// learn where it goes with the other, and let the handler decide whether to print the
// command or perform the write — rotini never touches the user's machine on its own:
//
//	t, err := rtk.CompletionInstallTarget(prog, shell)
//	if err != nil { /* handler owns it */ }
//	fmt.Printf("Install with:\n  %s\n", t.Command)
//	if t.Note != "" { fmt.Printf("\nNote: %s\n", t.Note) }
func CompletionInstallTarget(prog, shell string) (CompletionTarget, error) {
	switch shell {
	case "bash":
		dir, err := dataPath("bash-completion", "completions")
		if err != nil {
			return CompletionTarget{}, err
		}
		path := filepath.Join(dir, prog)
		return CompletionTarget{
			Shell:   shell,
			Path:    path,
			Command: fmt.Sprintf("mkdir -p %s && %s completion bash > %s", shellQuote(dir), prog, shellQuote(path)),
			Note:    "Requires the bash-completion runtime; start a new shell to load it.",
		}, nil
	case "zsh":
		dir, err := dataPath("zsh", "site-functions")
		if err != nil {
			return CompletionTarget{}, err
		}
		path := filepath.Join(dir, "_"+prog)
		return CompletionTarget{
			Shell:   shell,
			Path:    path,
			Command: fmt.Sprintf("mkdir -p %s && %s completion zsh > %s", shellQuote(dir), prog, shellQuote(path)),
			Note:    fmt.Sprintf("Ensure %s is on your $fpath (add it before compinit in ~/.zshrc), then start a new shell.", dir),
		}, nil
	case "fish":
		dir, err := configPath("fish", "completions")
		if err != nil {
			return CompletionTarget{}, err
		}
		path := filepath.Join(dir, prog+".fish")
		return CompletionTarget{
			Shell:   shell,
			Path:    path,
			Command: fmt.Sprintf("mkdir -p %s && %s completion fish > %s", shellQuote(dir), prog, shellQuote(path)),
			Note:    "Open a new fish shell to load it.",
		}, nil
	case "powershell":
		// PowerShell loads completions from $PROFILE — a shell-resolved path, not a fixed
		// file — so the script is appended there.
		return CompletionTarget{
			Shell:   shell,
			Path:    "$PROFILE",
			Command: fmt.Sprintf("%s completion powershell >> $PROFILE", prog),
			Note:    "Run in PowerShell; reopen PowerShell (or run `. $PROFILE`) to load it.",
		}, nil
	case "":
		return CompletionTarget{}, fmt.Errorf("a shell is required (bash, zsh, fish, or powershell)")
	default:
		return CompletionTarget{}, fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
}

// InstallCompletion writes prog's completion script for shell to its conventional
// location (the path [CompletionInstallTarget] resolves), creating the directory and
// writing the file atomically, and returns the path written. script is the script bytes —
// typically the program's embedded `Completion(shell)` resolver or [CompletionScript].
//
// Unlike [CompletionInstallTarget] (which only returns data), this DOES touch the user's
// machine — so it is something a handler calls deliberately, never something rotini runs on
// its own (Pillar 1). Confirmation is the handler's: prompt with [Prompter.Confirm] first if
// you want one; rotini neither prompts nor logs here. The write is atomic (temp file then
// rename, so an interruption never leaves a torn file) and the completion file is
// rotini-managed, so re-installing simply overwrites it.
//
// Installable shells are bash, zsh, and fish, whose completions live in a writable file.
// powershell loads completions from $PROFILE — a path the shell resolves, not one this can
// write — so it returns an error pointing at the manual command from CompletionInstallTarget.
//
//	script, _ := rtg.Completion(shell)            // the embedded script
//	path, err := rtk.InstallCompletion(prog, shell, script)
//	if err != nil { /* handler owns it */ }
//	pr.Success("installed %s completion to %s", shell, path)
func InstallCompletion(prog, shell, script string) (string, error) {
	switch shell {
	case "bash", "zsh", "fish":
		target, err := CompletionInstallTarget(prog, shell)
		if err != nil {
			return "", err
		}
		if err := fs.WriteString(target.Path, script, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
			return "", fmt.Errorf("rotini: install %s completion: %w", shell, err)
		}
		return target.Path, nil
	case "powershell":
		t, _ := CompletionInstallTarget(prog, "powershell")
		return "", fmt.Errorf("rotini: cannot auto-install powershell completion ($PROFILE is resolved by the shell); run:\n  %s", t.Command)
	case "":
		return "", fmt.Errorf("a shell is required (bash, zsh, or fish)")
	default:
		return "", fmt.Errorf("unsupported shell %q (installable: bash, zsh, fish)", shell)
	}
}

// configPath joins elems under the user's XDG config home ($XDG_CONFIG_HOME, else
// ~/.config) — the base the shells use on every platform, macOS included (unlike the
// OS-native config dir, which on macOS is ~/Library/Application Support).
func configPath(elems ...string) (string, error) {
	return xdgPath("XDG_CONFIG_HOME", []string{".config"}, elems)
}

// dataPath joins elems under the user's XDG data home ($XDG_DATA_HOME, else
// ~/.local/share), the base the shells read for completions on every platform.
func dataPath(elems ...string) (string, error) {
	return xdgPath("XDG_DATA_HOME", []string{".local", "share"}, elems)
}

// xdgPath resolves an XDG base directory (env, else the home-relative fallback) and joins
// elems beneath it.
func xdgPath(env string, fallback, elems []string) (string, error) {
	base := os.Getenv(env)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("rotini: locate home directory: %w", err)
		}
		base = filepath.Join(append([]string{home}, fallback...)...)
	}
	return filepath.Join(append([]string{base}, elems...)...), nil
}

// shellQuote wraps s in single quotes for safe copy-paste into a POSIX shell, escaping
// any embedded single quote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
