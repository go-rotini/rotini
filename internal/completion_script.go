package internal

import (
	"fmt"
	"strings"
)

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
		return "", fmt.Errorf("a shell is required (bash, zsh, fish, or powershell)")
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
	return strings.ReplaceAll(tmpl, "PROG", prog), nil
}

const bashCompletionTemplate = `# bash completion for PROG
_PROG_complete() {
    local args IFS=$'\n'
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=($(PROG __complete "${args[@]}" 2>/dev/null))
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
_PROG() {
    local -a completions
    completions=(${(f)"$(PROG __complete ${words[2,$CURRENT]} 2>/dev/null)"})
    compadd -a completions
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
function __PROG_complete
    set -l tokens (commandline -opc) (commandline -ct)
    PROG __complete $tokens[2..-1] 2>/dev/null
end
complete -c PROG -f -a '(__PROG_complete)'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    if ($wordToComplete -eq '') { $tokens += '' }
    PROG __complete @tokens 2>$null | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`
