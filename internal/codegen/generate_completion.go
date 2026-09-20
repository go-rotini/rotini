package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shell completion scripts (bash / zsh / fish / powershell).
// ─────────────────────────────────────────────────────────────────────────────.

// completionScript returns a shell completion script for prog and shell, delegating to the
// binary's hidden __complete entrypoint so completions always reflect the live command tree.
// Supported shells: bash, zsh, fish, powershell.
//
// It runs at codegen time: with the completion feature enabled, the generator renders one
// script per shell and embeds it in the cmd package.
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
#
# A final ":rotini:<directive>" line is the spec's declarative completion hint
# for the value being typed — file, directory, or none. It is handled here rather
# than offered as a candidate.
_PROG_complete() {
    local args line IFS=$'\n' directive="" ext exts
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=()
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        case "$line" in
            ":rotini:"*) directive="${line#:rotini:}" ;;
            *) COMPREPLY+=("${line%%$'\t'*}") ;;
        esac
    done

    case "$directive" in
        none)
            # An opaque value: suppress bash's default file fallback entirely.
            compopt +o default 2>/dev/null
            ;;
        directory)
            compopt -o dirnames 2>/dev/null
            COMPREPLY+=($(compgen -d -- "${COMP_WORDS[$COMP_CWORD]}"))
            ;;
        file)
            compopt -o filenames 2>/dev/null
            COMPREPLY+=($(compgen -f -- "${COMP_WORDS[$COMP_CWORD]}"))
            ;;
        file\ *)
            compopt -o filenames 2>/dev/null
            exts="${directive#file }"
            COMPREPLY+=($(compgen -d -- "${COMP_WORDS[$COMP_CWORD]}"))
            for ext in $exts; do
                COMPREPLY+=($(compgen -f -X "!*.$ext" -- "${COMP_WORDS[$COMP_CWORD]}"))
            done
            ;;
    esac
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# Candidates arrive as "name<TAB>description"; zsh renders the description
# beside the name via _describe (colons in either part are escaped).
_PROG() {
    local -a lines pairs exts
    local line name desc directive=""
    lines=(${(f)"$(PROG __complete ${words[2,$CURRENT]} 2>/dev/null)"})
    for line in $lines; do
        # A final ":rotini:<directive>" line is the spec's declarative hint for the
        # value being typed — file, directory, or none — not a candidate.
        if [[ $line == ':rotini:'* ]]; then
            directive=${line#:rotini:}
            continue
        fi
        if [[ $line == *$'\t'* ]]; then
            name=${line%%$'\t'*}
            desc=${line#*$'\t'}
            pairs+=("${name//:/\\:}:${desc//:/\\:}")
        else
            pairs+=("${line//:/\\:}")
        fi
    done
    (( $#pairs )) && _describe 'PROG' pairs

    case $directive in
        none) return 0 ;;                 # opaque value: offer nothing at all
        directory) _files -/ ;;
        file) _files ;;
        "file "*) exts=(${=directive#file }); _files -g "*.(${(j:|:)exts})" ;;
    esac
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
function __PROG_raw
    set -l tokens (commandline -opc) (commandline -ct)
    PROG __complete $tokens[2..-1] 2>/dev/null
end

# Split the binary's output into candidates and the trailing ":rotini:<directive>"
# line, which carries the spec's declarative hint for the value being typed.
function __PROG_load
    set -g __PROG_results
    set -g __PROG_directive ""
    for line in (__PROG_raw)
        if string match -q ':rotini:*' -- $line
            set -g __PROG_directive (string replace ':rotini:' '' -- $line)
        else
            set -a __PROG_results $line
        end
    end
end

function __PROG_has_results
    __PROG_load
    test (count $__PROG_results) -gt 0
end

# "none" means the value is opaque, so fish must offer nothing — not even files.
function __PROG_wants_files
    __PROG_load
    test (count $__PROG_results) -eq 0; and test "$__PROG_directive" != none
end

function __PROG_files
    __PROG_load
    switch $__PROG_directive
        case directory
            __fish_complete_directories (commandline -ct)
        case 'file *'
            for ext in (string split ' ' -- (string replace 'file ' '' -- $__PROG_directive))
                __fish_complete_suffix ".$ext"
            end
        case '*'
            __fish_complete_path (commandline -ct)
    end
end

# Offer the binary's candidates when it has any; otherwise the paths the hint asks
# for. Candidates arrive as "name<TAB>description" — fish renders that natively.
complete -c PROG -f -n '__PROG_has_results' -a '$__PROG_results'
complete -c PROG -f -n '__PROG_wants_files' -a '(__PROG_files)'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    if ($wordToComplete -eq '') { $tokens += '' }
    $lines = @(PROG __complete @tokens 2>$null)
    # A final ":rotini:<directive>" line is the spec's declarative hint for the value
    # being typed; everything else is a candidate.
    $directive = ($lines | Where-Object { $_ -like ':rotini:*' } | Select-Object -Last 1)
    $candidates = $lines | Where-Object { $_ -notlike ':rotini:*' }

    $candidates | ForEach-Object {
        # Candidates arrive as "name<TAB>description"; the description becomes
        # the CompletionResult tooltip.
        $parts = $_ -split "` + "`" + `t", 2
        $text = $parts[0]
        $tip = if ($parts.Count -gt 1 -and $parts[1]) { $parts[1] } else { $text }
        [System.Management.Automation.CompletionResult]::new($text, $text, 'ParameterValue', $tip)
    }

    if ($directive) {
        $hint = $directive -replace '^:rotini:', ''
        switch -Wildcard ($hint) {
            'none' { return }
            'directory' {
                Get-ChildItem -Directory -Filter "$wordToComplete*" -ErrorAction SilentlyContinue |
                    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_.Name, $_.Name, 'ProviderContainer', $_.FullName) }
            }
            'file *' {
                foreach ($ext in ($hint -replace '^file ', '') -split ' ') {
                    Get-ChildItem -File -Filter "$wordToComplete*.$ext" -ErrorAction SilentlyContinue |
                        ForEach-Object { [System.Management.Automation.CompletionResult]::new($_.Name, $_.Name, 'ProviderItem', $_.FullName) }
                }
            }
            'file' {
                Get-ChildItem -Filter "$wordToComplete*" -ErrorAction SilentlyContinue |
                    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_.Name, $_.Name, 'ProviderItem', $_.FullName) }
            }
        }
    }
}
`
