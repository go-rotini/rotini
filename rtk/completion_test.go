package rtk_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Fixture spec used by the per-shell tests
// =============================================================================

// completionFixture returns a small but representative spec: a root
// flag (bool + string with enum), two top-level commands, one with
// nested children, and one with aliases. Each shell test pins specific
// substrings — the spec is shared so a single change to a flag's
// declaration is reflected in every shell's output assertions.
func completionFixture() rtk.ProgramSpec {
	return rtk.ProgramSpec{
		Name: "todo",
		Flags: []rtk.FlagSpec{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"},
			{Name: "output", Identifiers: []string{"-o", "--output"}, Type: "string", Enum: []string{"table", "json", "yaml"}},
		},
		Commands: []rtk.CommandSpec{
			{
				Path: "add", Name: "add",
				Flags: []rtk.FlagSpec{
					{Name: "priority", Identifiers: []string{"-p"}, Type: "int"},
				},
			},
			{
				Path: "list", Name: "list", Aliases: []string{"ls", "l"},
				Commands: []rtk.CommandSpec{
					{Path: "list-open", Name: "open"},
				},
			},
		},
	}
}

// =============================================================================
// Dispatcher
// =============================================================================

func TestGenerateCompletion_dispatchesBySupportedShell(t *testing.T) {
	t.Parallel()
	spec := completionFixture()
	for _, shell := range rtk.SupportedShells {
		t.Run(string(shell), func(t *testing.T) {
			t.Parallel()
			script, err := rtk.GenerateCompletion(shell, spec)
			if err != nil {
				t.Fatalf("GenerateCompletion(%s): %v", shell, err)
			}
			if script == "" {
				t.Errorf("GenerateCompletion(%s): empty script", shell)
			}
		})
	}
}

func TestGenerateCompletion_unknownShellSentinel(t *testing.T) {
	t.Parallel()
	_, err := rtk.GenerateCompletion("xonsh", completionFixture())
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
	if !errors.Is(err, rtk.ErrUnknownShell) {
		t.Errorf("err is not ErrUnknownShell: %v", err)
	}
}

func TestGenerateCompletion_supportedShellsListIsCoherent(t *testing.T) {
	t.Parallel()
	// Every value in SupportedShells must round-trip through
	// GenerateCompletion without ErrUnknownShell.
	for _, shell := range rtk.SupportedShells {
		if _, err := rtk.GenerateCompletion(shell, completionFixture()); errors.Is(err, rtk.ErrUnknownShell) {
			t.Errorf("SupportedShells lists %q but dispatcher rejects it", shell)
		}
	}
}

// =============================================================================
// Bash — structure of the emitted script
// =============================================================================

func TestGenerateCompletion_bashStructure(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellBash, completionFixture())
	wants := []string{
		`_todo() {`,                      // function declaration uses dashes-as-underscores
		`local cur prev words cword`,     // bash-completion prologue
		`_init_completion || return`,     // hard requirement for bash-completion users
		`add) cmd="add"; break;;`,        // command match arm for "add"
		`list|ls|l) cmd="list"; break;;`, // aliases pipe-joined
		`open) cmd="open"; break;;`,      // nested command appears as flattened entry
		`compgen -W`,                     // candidate list helper
		`if [[ "$prev" == "-o" ]]`,       // enum case for --output's short form
		`if [[ "$prev" == "--output" ]]`, // enum case for long form
		`compgen -W "table json yaml"`,   // enum value list
		`complete -F _todo todo`,         // final registration
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("bash script missing %q\n--- script ---\n%s", w, script)
		}
	}
}

// =============================================================================
// Zsh — structure of the emitted script
// =============================================================================

func TestGenerateCompletion_zshStructure(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellZsh, completionFixture())
	wants := []string{
		`_todo() {`,
		`commands=(`,
		`'add'`,
		`'list'`,
		`'ls'`, // alias
		`_arguments -s`,
		`'-v' `,                          // bool flag short form
		`'--verbose' `,                   // bool flag long form
		`'-o[]:value:(table json yaml)'`, // enum-typed short
		`'--output[]:value:(table json yaml)'`,
		`compdef _todo todo`,
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("zsh script missing %q\n--- script ---\n%s", w, script)
		}
	}
}

// =============================================================================
// Fish — structure of the emitted script
// =============================================================================

func TestGenerateCompletion_fishStructure(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellFish, completionFixture())
	wants := []string{
		`complete -f -c todo -n '__fish_use_subcommand' -s 'v'`, // root short bool
		`complete -f -c todo -n '__fish_use_subcommand' -l 'verbose'`,
		`complete -f -c todo -n '__fish_use_subcommand' -l 'output' -r -a 'table json yaml'`,
		`-n '__fish_use_subcommand' -a 'add'`,   // command registration
		`-n '__fish_use_subcommand' -a 'ls'`,    // alias registration
		`__fish_seen_subcommand_from list ls l`, // alias-aware condition
		`-n '__fish_seen_subcommand_from add'`,  // per-command flag context
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("fish script missing %q\n--- script ---\n%s", w, script)
		}
	}
}

// =============================================================================
// PowerShell / Nushell / Elvish — smoke checks
//
// The three less-common shells get cheaper coverage: just confirm the
// script registers under the program's name and references the
// command set. Detailed assertions per dialect aren't worth the
// maintenance overhead at this stage.
// =============================================================================

func TestGenerateCompletion_powershellSmoke(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellPowershell, completionFixture())
	for _, w := range []string{
		`Register-ArgumentCompleter -CommandName 'todo'`,
		`switch ($cmd) {`,
		`CompletionResult`,
		`'add'`,
		`'list'`,
	} {
		if !strings.Contains(script, w) {
			t.Errorf("powershell script missing %q", w)
		}
	}
}

func TestGenerateCompletion_nushellSmoke(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellNushell, completionFixture())
	for _, w := range []string{
		`export extern "todo" [`,
		`export extern "todo add" [`,
		`--verbose(-v)`,
		`--output(-o): string@"table|json|yaml"`,
	} {
		if !strings.Contains(script, w) {
			t.Errorf("nushell script missing %q", w)
		}
	}
}

func TestGenerateCompletion_elvishSmoke(t *testing.T) {
	t.Parallel()
	script, _ := rtk.GenerateCompletion(rtk.ShellElvish, completionFixture())
	for _, w := range []string{
		`set edit:completion:arg-completer[todo] =`,
		`if (eq $cmd '')`,
		`put add`,
		`put list`,
	} {
		if !strings.Contains(script, w) {
			t.Errorf("elvish script missing %q", w)
		}
	}
}

// =============================================================================
// Determinism — multiple runs produce identical output
// =============================================================================

func TestGenerateCompletion_isDeterministic(t *testing.T) {
	t.Parallel()
	spec := completionFixture()
	for _, shell := range rtk.SupportedShells {
		t.Run(string(shell), func(t *testing.T) {
			t.Parallel()
			a, _ := rtk.GenerateCompletion(shell, spec)
			b, _ := rtk.GenerateCompletion(shell, spec)
			if a != b {
				t.Errorf("non-deterministic output for %q", shell)
			}
		})
	}
}

// =============================================================================
// Empty / minimal spec — gen functions must not panic
// =============================================================================

func TestGenerateCompletion_minimalSpec(t *testing.T) {
	t.Parallel()
	// One command is the minimum that produces non-empty output for
	// every supported shell — fish in particular emits nothing for a
	// spec with no commands AND no flags (there's nothing to register).
	spec := rtk.ProgramSpec{
		Name: "minimal",
		Commands: []rtk.CommandSpec{
			{Path: "run", Name: "run"},
		},
	}
	for _, shell := range rtk.SupportedShells {
		t.Run(string(shell), func(t *testing.T) {
			t.Parallel()
			script, err := rtk.GenerateCompletion(shell, spec)
			if err != nil {
				t.Fatalf("err for minimal spec: %v", err)
			}
			if !strings.Contains(script, "minimal") {
				t.Errorf("minimal-spec %q script does not reference program name", shell)
			}
		})
	}
}
