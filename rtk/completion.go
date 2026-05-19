package rtk

import (
	"errors"
	"fmt"
	"strings"
)

// Shell identifies the target shell flavor for a completion script.
type Shell string

const (
	ShellBash       Shell = "bash"
	ShellZsh        Shell = "zsh"
	ShellFish       Shell = "fish"
	ShellPowershell Shell = "powershell"
	ShellNushell    Shell = "nushell"
	ShellElvish     Shell = "elvish"
)

// SupportedShells lists every Shell value [GenerateCompletion] knows
// how to emit. Useful for `rotini completion --help` and similar.
var SupportedShells = []Shell{
	ShellBash, ShellZsh, ShellFish, ShellPowershell, ShellNushell, ShellElvish,
}

// ErrUnknownShell is returned by [GenerateCompletion] when the supplied
// shell name is not one of the values in [SupportedShells].
var ErrUnknownShell = errors.New("rtk: unknown shell")

// GenerateCompletion dispatches to the per-shell completion-script
// generator and returns the rendered script. Unknown shells return
// [ErrUnknownShell] wrapped with the supplied name.
//
// The script is a complete, ready-to-source shell snippet. Callers
// typically write it to stdout in response to a
// `<program> completion <shell>` invocation; users redirect that into
// their shell's completion directory.
func GenerateCompletion(shell Shell, spec ProgramSpec) (string, error) {
	switch shell {
	case ShellBash:
		return generateBashCompletion(spec), nil
	case ShellZsh:
		return generateZshCompletion(spec), nil
	case ShellFish:
		return generateFishCompletion(spec), nil
	case ShellPowershell:
		return generatePowershellCompletion(spec), nil
	case ShellNushell:
		return generateNushellCompletion(spec), nil
	case ShellElvish:
		return generateElvishCompletion(spec), nil
	default:
		return "", fmt.Errorf("%w: %q (supported: %v)", ErrUnknownShell, shell, SupportedShells)
	}
}

// generateBashCompletion emits a bash completion script for spec. The
// resulting script defines a `_<name>` completion function and
// registers it via `complete -F`.
func generateBashCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name
	fnName := strings.ReplaceAll(name, "-", "_")

	fmt.Fprintf(&b, "_%s() {\n", fnName)
	b.WriteString("    local cur prev words cword\n")
	b.WriteString("    _init_completion || return\n\n")

	allCommands := collectCommandEntries(spec.Commands)

	b.WriteString("    local cmd=\"\"\n")
	b.WriteString("    for ((i=1; i < cword; i++)); do\n")
	b.WriteString("        case \"${words[i]}\" in\n")
	for _, entry := range allCommands {
		names := append([]string{entry.name}, entry.aliases...)
		b.WriteString("            ")
		b.WriteString(strings.Join(names, "|"))
		fmt.Fprintf(&b, ") cmd=%q; break;;\n", entry.name)
	}
	b.WriteString("            -*) ;;\n")
	b.WriteString("            *) break;;\n")
	b.WriteString("        esac\n")
	b.WriteString("    done\n\n")

	b.WriteString("    case \"$cmd\" in\n")

	rootFlags := collectFlagIdentifiers(spec.Flags)
	commandNames := collectTopLevelNames(spec.Commands)
	fmt.Fprintf(&b, "        \"\")\n")
	bashEnumCases(&b, spec.Flags)
	fmt.Fprintf(&b, "            COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(append(commandNames, rootFlags...), " "))
	b.WriteString("            ;;\n")

	for _, entry := range allCommands {
		flags := collectFlagIdentifiers(entry.flags)
		fmt.Fprintf(&b, "        %q)\n", entry.name)
		bashEnumCases(&b, entry.flags)
		fmt.Fprintf(&b, "            COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(flags, " "))
		b.WriteString("            ;;\n")
	}

	b.WriteString("    esac\n")
	fmt.Fprintf(&b, "}\n\ncomplete -F _%s %s\n", fnName, name)

	return b.String()
}

// generateZshCompletion emits a zsh completion function via the
// `compdef` mechanism.
func generateZshCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name
	fnName := strings.ReplaceAll(name, "-", "_")

	fmt.Fprintf(&b, "_%s() {\n", fnName)
	b.WriteString("    local -a commands\n")
	b.WriteString("    commands=(\n")
	for _, cmd := range spec.Commands {
		fmt.Fprintf(&b, "        '%s'\n", cmd.Name)
		for _, alias := range cmd.Aliases {
			fmt.Fprintf(&b, "        '%s'\n", alias)
		}
	}
	b.WriteString("    )\n\n")

	b.WriteString("    _arguments -s \\\n")
	for _, flag := range spec.Flags {
		zshFlagArgs(&b, flag)
	}
	b.WriteString("        '1:command:->command' \\\n")
	b.WriteString("        '*::arg:->args'\n\n")

	b.WriteString("    case $state in\n")
	b.WriteString("        command)\n")
	b.WriteString("            _describe 'command' commands\n")
	b.WriteString("            ;;\n")
	b.WriteString("        args)\n")
	b.WriteString("            case $words[1] in\n")

	allCommands := collectCommandEntries(spec.Commands)
	for _, entry := range allCommands {
		names := append([]string{entry.name}, entry.aliases...)
		fmt.Fprintf(&b, "                %s)\n", strings.Join(names, "|"))
		b.WriteString("                    _arguments -s \\\n")
		for _, flag := range entry.flags {
			zshFlagArgs(&b, flag)
		}
		b.WriteString("                        '*:'\n")
		b.WriteString("                    ;;\n")
	}

	b.WriteString("            esac\n")
	b.WriteString("            ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "compdef _%s %s\n", fnName, name)

	return b.String()
}

// generateFishCompletion emits a sequence of `complete` directives
// for the fish shell.
func generateFishCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name

	allCommands := collectCommandEntries(spec.Commands)

	for _, flag := range spec.Flags {
		fishFlagLine(&b, name, "__fish_use_subcommand", flag)
	}

	for _, cmd := range spec.Commands {
		fmt.Fprintf(&b, "complete -f -c %s -n '__fish_use_subcommand' -a '%s'\n", name, cmd.Name)
		for _, alias := range cmd.Aliases {
			fmt.Fprintf(&b, "complete -f -c %s -n '__fish_use_subcommand' -a '%s'\n", name, alias)
		}
	}

	for _, entry := range allCommands {
		seen := entry.name
		if len(entry.aliases) > 0 {
			seen = strings.Join(append([]string{entry.name}, entry.aliases...), " ")
		}
		condition := fmt.Sprintf("__fish_seen_subcommand_from %s", seen)

		fmt.Fprintf(&b, "complete -f -c %s -n '%s'\n", name, condition)

		for _, flag := range entry.flags {
			fishFlagLine(&b, name, condition, flag)
		}
	}

	return b.String()
}

// generatePowershellCompletion emits a PowerShell completion script
// registered via Register-ArgumentCompleter.
func generatePowershellCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name

	allCommands := collectCommandEntries(spec.Commands)
	commandNames := collectTopLevelNames(spec.Commands)

	b.WriteString("$scriptBlock = {\n")
	b.WriteString("    param($wordToComplete, $commandAst, $cursorPosition)\n\n")
	b.WriteString("    $tokens = $commandAst.ToString().Split()\n")
	b.WriteString("    $cmd = ''\n")
	b.WriteString("    for ($i = 1; $i -lt $tokens.Length; $i++) {\n")
	b.WriteString("        $t = $tokens[$i]\n")
	b.WriteString("        if ($t -notlike '-*') {\n")
	b.WriteString("            $cmd = $t\n")
	b.WriteString("            break\n")
	b.WriteString("        }\n")
	b.WriteString("    }\n\n")

	b.WriteString("    switch ($cmd) {\n")

	rootFlags := collectFlagIdentifiers(spec.Flags)
	rootCompletions := make([]string, 0, len(commandNames)+len(rootFlags))
	rootCompletions = append(rootCompletions, commandNames...)
	rootCompletions = append(rootCompletions, rootFlags...)
	b.WriteString("        '' {\n")
	for _, c := range rootCompletions {
		fmt.Fprintf(&b, "            [System.Management.Automation.CompletionResult]::new('%s', '%s', 'ParameterValue', '%s')\n", c, c, c)
	}
	b.WriteString("        }\n")

	for _, entry := range allCommands {
		names := append([]string{entry.name}, entry.aliases...)
		fmt.Fprintf(&b, "        { $_ -in @(%s) } {\n", psStringArray(names))
		for _, flag := range entry.flags {
			for _, id := range flag.Identifiers {
				label := id
				if len(flag.Enum) > 0 {
					label = fmt.Sprintf("%s [%s]", id, strings.Join(flag.Enum, "|"))
				}
				fmt.Fprintf(&b, "            [System.Management.Automation.CompletionResult]::new('%s', '%s', 'ParameterValue', '%s')\n", id, id, label)
			}
		}
		b.WriteString("        }\n")
	}

	b.WriteString("    }\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "Register-ArgumentCompleter -CommandName '%s' -Native -ScriptBlock $scriptBlock\n", name)

	return b.String()
}

// generateNushellCompletion emits Nushell `extern` declarations for
// the program and each command.
func generateNushellCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name

	allCommands := collectCommandEntries(spec.Commands)
	commandNames := collectTopLevelNames(spec.Commands)

	fmt.Fprintf(&b, "def \"%s commands\" [] {\n", name)
	b.WriteString("    [\n")
	for _, n := range commandNames {
		fmt.Fprintf(&b, "        \"%s\"\n", n)
	}
	b.WriteString("    ]\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "export extern \"%s\" [\n", name)
	for _, flag := range spec.Flags {
		nushellFlagLine(&b, flag)
	}
	b.WriteString("]\n")

	for _, entry := range allCommands {
		fmt.Fprintf(&b, "\nexport extern \"%s %s\" [\n", name, entry.name)
		for _, flag := range entry.flags {
			nushellFlagLine(&b, flag)
		}
		b.WriteString("]\n")
	}

	return b.String()
}

// generateElvishCompletion emits an Elvish completion-arg-completer
// for the program.
func generateElvishCompletion(spec ProgramSpec) string {
	var b strings.Builder
	name := spec.Name

	allCommands := collectCommandEntries(spec.Commands)
	commandNames := collectTopLevelNames(spec.Commands)
	rootFlags := collectFlagIdentifiers(spec.Flags)

	fmt.Fprintf(&b, "set edit:completion:arg-completer[%s] = {|@args|\n", name)
	b.WriteString("    var n = (count $args)\n")
	b.WriteString("    var cmd = ''\n")
	b.WriteString("    for i [(range 1 $n)] {\n")
	b.WriteString("        var t = $args[$i]\n")
	b.WriteString("        if (not (str:has-prefix $t '-')) {\n")
	b.WriteString("            set cmd = $t\n")
	b.WriteString("            break\n")
	b.WriteString("        }\n")
	b.WriteString("    }\n\n")

	b.WriteString("    if (eq $cmd '') {\n")
	for _, n := range commandNames {
		fmt.Fprintf(&b, "        put %s\n", n)
	}
	for _, f := range rootFlags {
		fmt.Fprintf(&b, "        put %s\n", f)
	}
	b.WriteString("    }")

	for _, entry := range allCommands {
		names := append([]string{entry.name}, entry.aliases...)
		conditions := make([]string, len(names))
		for i, n := range names {
			conditions[i] = fmt.Sprintf("(eq $cmd %s)", n)
		}
		fmt.Fprintf(&b, " elif (or %s) {\n", strings.Join(conditions, " "))
		flags := collectFlagIdentifiers(entry.flags)
		for _, f := range flags {
			fmt.Fprintf(&b, "        put %s\n", f)
		}
		if len(entry.flags) == 0 {
			b.WriteString("        # no flags\n")
		}
		b.WriteString("    }")
	}

	b.WriteString("\n}\n")

	return b.String()
}

// commandEntry holds the per-command data the shell-gen functions
// need: command name, aliases, and the flag set declared for that
// command. Compared to walking [CommandSpec] directly, this struct
// gives the gen functions a uniform view they can iterate.
type commandEntry struct {
	name    string
	aliases []string
	flags   []FlagSpec
}

// collectCommandEntries flattens the command tree to one entry per
// command (root excluded). Nested commands appear after their parent
// in the resulting slice, in declaration order.
func collectCommandEntries(commands []CommandSpec) []commandEntry {
	var entries []commandEntry
	var walk func(cmds []CommandSpec)
	walk = func(cmds []CommandSpec) {
		for i := range cmds {
			c := &cmds[i]
			entries = append(entries, commandEntry{
				name:    c.Name,
				aliases: c.Aliases,
				flags:   c.Flags,
			})
			walk(c.Commands)
		}
	}
	walk(commands)
	return entries
}

// collectTopLevelNames returns the names + aliases of every
// top-level command. Used by the bash/powershell/nushell/elvish
// generators when emitting the root-completion case.
func collectTopLevelNames(commands []CommandSpec) []string {
	var names []string
	for _, cmd := range commands {
		names = append(names, cmd.Name)
		names = append(names, cmd.Aliases...)
	}
	return names
}

// collectFlagIdentifiers returns every CLI identifier across the
// supplied flag set. Used by the bash/powershell/elvish generators
// when emitting flag-completion candidates.
func collectFlagIdentifiers(flags []FlagSpec) []string {
	var ids []string
	for _, f := range flags {
		ids = append(ids, f.Identifiers...)
	}
	return ids
}

// bashEnumCases emits `if [[ "$prev" == "<id>" ]]` blocks for every
// enum-constrained flag. Bash completions use these to suggest enum
// values when the user types e.g. `--output `.
func bashEnumCases(b *strings.Builder, flags []FlagSpec) {
	for _, f := range flags {
		if len(f.Enum) == 0 {
			continue
		}
		for _, id := range f.Identifiers {
			fmt.Fprintf(b, "            if [[ \"$prev\" == %q ]]; then\n", id)
			fmt.Fprintf(b, "                COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(f.Enum, " "))
			b.WriteString("                return\n")
			b.WriteString("            fi\n")
		}
	}
}

// zshFlagArgs emits a zsh `_arguments`-compatible line per identifier
// for a flag.
func zshFlagArgs(b *strings.Builder, flag FlagSpec) {
	for _, id := range flag.Identifiers {
		switch {
		case flag.Type == "bool":
			fmt.Fprintf(b, "                        '%s' \\\n", id)
		case len(flag.Enum) > 0:
			fmt.Fprintf(b, "                        '%s[]:value:(%s)' \\\n", id, strings.Join(flag.Enum, " "))
		default:
			fmt.Fprintf(b, "                        '%s[]:value:' \\\n", id)
		}
	}
}

// fishFlagLine emits a `complete` directive per identifier for a flag,
// scoped to the supplied condition.
func fishFlagLine(b *strings.Builder, name, condition string, flag FlagSpec) {
	for _, id := range flag.Identifiers {
		isShort := strings.HasPrefix(id, "-") && !strings.HasPrefix(id, "--")
		flagName := strings.TrimLeft(id, "-")

		b.WriteString("complete -f -c ")
		b.WriteString(name)
		fmt.Fprintf(b, " -n '%s'", condition)
		if isShort {
			fmt.Fprintf(b, " -s '%s'", flagName)
		} else {
			fmt.Fprintf(b, " -l '%s'", flagName)
		}
		if flag.Type != "bool" {
			b.WriteString(" -r")
			if len(flag.Enum) > 0 {
				fmt.Fprintf(b, " -a '%s'", strings.Join(flag.Enum, " "))
			}
		}
		b.WriteString("\n")
	}
}

// nushellFlagLine emits a Nushell extern flag declaration for a flag.
// Combines long + short identifiers into a single declaration; emits
// an enum-typed signature when the flag has Enum values.
func nushellFlagLine(b *strings.Builder, flag FlagSpec) {
	var longName, shortName string
	for _, id := range flag.Identifiers {
		if rest, ok := strings.CutPrefix(id, "--"); ok {
			longName = rest
		} else if rest, ok := strings.CutPrefix(id, "-"); ok {
			shortName = rest
		}
	}

	if longName == "" && shortName == "" {
		return
	}
	if longName == "" {
		longName = shortName
	}

	fmt.Fprintf(b, "    --%s", longName)
	if shortName != "" {
		fmt.Fprintf(b, "(-%s)", shortName)
	}

	switch {
	case flag.Type == "bool":
		b.WriteString("\n")
	case len(flag.Enum) > 0:
		fmt.Fprintf(b, ": string@\"%s\"\n", strings.Join(flag.Enum, "|"))
	default:
		b.WriteString(": string\n")
	}
}

// psStringArray renders a []string as a PowerShell-style
// comma-separated quoted-string list.
func psStringArray(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("'%s'", v)
	}
	return strings.Join(quoted, ", ")
}
