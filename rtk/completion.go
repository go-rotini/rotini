package rtk

import (
	"fmt"
	"strings"
)

// CompletionScript returns a shell completion script for prog (the installed
// binary name) and shell. The script delegates to the binary's hidden completion
// entrypoint (the core runtime's __complete intercept), so completions always
// reflect the live command tree. Supported shells: bash, zsh, fish.
//
// A completion handler typically reads the requested shell from its inputs and
// prints the result:
//
//	script, err := rtk.CompletionScript(filepath.Base(os.Args[0]), shell)
func CompletionScript(prog, shell string) (string, error) {
	var tmpl string
	switch shell {
	case "bash":
		tmpl = bashCompletionTemplate
	case "zsh":
		tmpl = zshCompletionTemplate
	case "fish":
		tmpl = fishCompletionTemplate
	case "":
		return "", fmt.Errorf("a shell is required (bash, zsh, or fish)")
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell)
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
