package rotini

import (
	"fmt"
	"sort"
	"strings"
)

// completeCommand is the hidden sub-command the generated shell scripts call to
// ask the binary for completion candidates.
const completeCommand = "__complete"

// complete returns the completion candidates for the word currently being typed
// (the last element of words; the rest are the preceding context). It completes
// flag values (enum), flag names (when the word starts with "-"), and otherwise
// sub-command / remote-command names — all filtered by the typed prefix.
func complete(def Definition, words []string) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	partial := words[len(words)-1]
	context := words[:len(words)-1]

	// Resolve the command chain from the context words, leniently skipping
	// anything that isn't a known sub-command (flags, flag values, arguments).
	chain := []frame{rootFrame(def)}
	for _, w := range context {
		if isFlag(w) {
			continue
		}
		if child, ok := findChild(chain[len(chain)-1], w); ok {
			chain = append(chain, cmdFrame(child))
		}
	}
	cur := chain[len(chain)-1]

	// Completing the value of the preceding flag → offer its enum.
	if len(context) > 0 {
		if prev := context[len(context)-1]; isFlag(prev) {
			name, _, _ := splitFlag(prev)
			if fd, _, ok := findFlag(chain, name); ok && fd.Type != "bool" && len(fd.Enum) > 0 {
				return filterPrefix(fd.Enum, partial)
			}
		}
	}

	// Completing a flag name.
	if strings.HasPrefix(partial, "-") {
		ids := []string{"-h", "--help"}
		for i := len(chain) - 1; i >= 0; i-- {
			for _, f := range chain[i].flags {
				ids = append(ids, f.Identifiers...)
			}
		}
		return filterPrefix(ids, partial)
	}

	// Completing a sub-command or remote-command name.
	var names []string
	for _, c := range cur.commands {
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	for _, r := range cur.remotes {
		names = append(names, r.Name)
		names = append(names, r.Aliases...)
	}
	return filterPrefix(names, partial)
}

func filterPrefix(candidates []string, prefix string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != "" && strings.HasPrefix(c, prefix) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// CompletionScript returns a shell completion script for prog (the installed
// binary name) and shell. The script delegates to the binary's hidden
// completion entrypoint, so completions always reflect the live command tree.
// Supported shells: bash, zsh, fish.
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
