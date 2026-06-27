package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shell completion scripts (bash / zsh / fish / powershell).
// ─────────────────────────────────────────────────────────────────────────────.

// completionScript returns a shell completion script for prog (the installed
// binary name) and shell. The script delegates to the binary's hidden completion
// entrypoint (the runtime's __complete intercept), so completions always reflect
// the live command tree. Supported shells: bash, zsh, fish, powershell.
//
// It is used at codegen time: when the completion feature is enabled, the
// generator renders one script per supported shell and embeds it in the cligen
// package (the runtime then serves the embedded script, never calling this).
func completionScript(prog, shell string) (string, error) {
	var tmpl string
	switch shell {
	case "bash":
		tmpl = bashCompletionTemplate
	case "zsh":
		tmpl = zshCompletionTemplate
	case "fish":
		tmpl = fishCompletionTemplate
	case "powershell":
		tmpl = powershellCompletionTemplate
	case "":
		return "", errors.New("a shell is required (bash, zsh, fish, or powershell)")
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
	return strings.ReplaceAll(tmpl, "PROG", prog), nil
}

const bashCompletionTemplate = `# bash completion for PROG
# Candidates arrive as "name<TAB>description"; bash cannot render descriptions,
# so everything from the first tab is stripped.
_PROG_complete() {
    local args line IFS=$'\n'
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=()
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        COMPREPLY+=("${line%%$'\t'*}")
    done
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# Candidates arrive as "name<TAB>description"; zsh renders the description
# beside the name via _describe (colons in either part are escaped).
_PROG() {
    local -a lines pairs
    local line name desc
    lines=(${(f)"$(PROG __complete ${words[2,$CURRENT]} 2>/dev/null)"})
    for line in $lines; do
        if [[ $line == *$'\t'* ]]; then
            name=${line%%$'\t'*}
            desc=${line#*$'\t'}
            pairs+=("${name//:/\\:}:${desc//:/\\:}")
        else
            pairs+=("${line//:/\\:}")
        fi
    done
    _describe 'PROG' pairs
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
function __PROG_complete
    set -l tokens (commandline -opc) (commandline -ct)
    PROG __complete $tokens[2..-1] 2>/dev/null
end

function __PROG_has_results
    set -g __PROG_results (__PROG_complete)
    test (count $__PROG_results) -gt 0
end

# Offer the binary's candidates when it has any; otherwise fall back to fish's
# file completion (the binary returns nothing for path-valued flags and
# arguments, exactly so the shell takes over). Candidates arrive as
# "name<TAB>description" — fish renders that shape natively.
complete -c PROG -f -n '__PROG_has_results' -a '$__PROG_results'
complete -c PROG -F -n 'not __PROG_has_results'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    if ($wordToComplete -eq '') { $tokens += '' }
    PROG __complete @tokens 2>$null | ForEach-Object {
        # Candidates arrive as "name<TAB>description"; the description becomes
        # the CompletionResult tooltip.
        $parts = $_ -split "` + "`" + `t", 2
        $text = $parts[0]
        $tip = if ($parts.Count -gt 1 -and $parts[1]) { $parts[1] } else { $text }
        [System.Management.Automation.CompletionResult]::new($text, $text, 'ParameterValue', $tip)
    }
}
`
