package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// completionScript returns the completion script for prog in shell (bash, zsh, fish or
// powershell). Each script delegates to the binary's hidden __complete command, so completions
// track the live command tree. The completion feature renders one script per shell at
// generate time. messagesEnv, when set, is the variable that hides completion messages, named
// in the script's header for the users who install it.
func completionScript(prog, shell, messagesEnv string) (string, error) {
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
	script := strings.ReplaceAll(tmpl, "PROG", prog)
	if messagesEnv != "" {
		title := " completion for " + prog + "\n"
		head, rest, _ := strings.Cut(script, title)
		script = head + title + "# Set " + messagesEnv + "=off to hide completion messages.\n" + rest
	}
	return script, nil
}

const bashCompletionTemplate = `# bash completion for PROG
# Completion messages show on the second TAB in bash 4.4 or later.

# compopt is missing in bash 3.2 (macOS's /bin/bash) and fails outside a completion.
_PROG_compopt() {
    if type compopt >/dev/null 2>&1; then
        compopt "$@" 2>/dev/null || true
    fi
    return 0
}

# Prints messages on the second TAB (COMP_TYPE 63), then "--" before the candidates bash
# lists, or the prompt again when there are none. $1 is the directive.
_PROG_show_messages() {
    (( ${#__PROG_messages[@]} )) || return 0
    [[ $COMP_TYPE == 63 ]] || return 0
    _PROG_messages_supported || return 0
    if (( ! ${#COMPREPLY[@]} )) && [[ $1 != none ]]; then
        _PROG_compopt -o filenames
        COMPREPLY=($(compgen -f -- "${COMP_WORDS[$COMP_CWORD]}")) || true
    fi
    _PROG_print_messages
    if (( ${#COMPREPLY[@]} )); then
        printf -- '--'
    else
        printf '%s' "${PS1@P}${COMP_LINE}"
    fi
    return 0
}

# ${PS1@P} needs bash 4.4 or later.
_PROG_messages_supported() {
    (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) ))
}

_PROG_print_messages() {
    printf '\n'
    printf '%s\n' "${__PROG_messages[@]}"
}

_PROG_complete() {
    local args line directive="" ext exts
    __PROG_messages=()
    # Slice before narrowing IFS: bash 3.2 joins the slice into one word otherwise.
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=()
    local IFS=$'\n'
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines,
    # then a ":rotini:<directive>" line (none, file, file <exts>, directory).
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        case "$line" in
            ":rotini:message "*) __PROG_messages+=("${line#:rotini:message }") ;;
            ":rotini:"*) directive="${line#:rotini:}" ;;
            *) COMPREPLY+=("${line%%$'\t'*}") ;;
        esac
    done

    case "$directive" in
        none)
            _PROG_compopt +o default
            ;;
        directory)
            _PROG_compopt -o filenames
            COMPREPLY+=($(compgen -d -- "${COMP_WORDS[$COMP_CWORD]}")) || true
            ;;
        file)
            _PROG_compopt -o filenames
            COMPREPLY+=($(compgen -f -- "${COMP_WORDS[$COMP_CWORD]}")) || true
            ;;
        file\ *)
            _PROG_compopt -o filenames
            exts="${directive#file }"
            COMPREPLY+=($(compgen -d -- "${COMP_WORDS[$COMP_CWORD]}")) || true
            for ext in ${exts// /$'\n'}; do
                COMPREPLY+=($(compgen -f -X "!*.$ext" -- "${COMP_WORDS[$COMP_CWORD]}")) || true
            done
            ;;
    esac
    _PROG_show_messages "$directive"
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# zsh completion for PROG
_PROG() {
    local -a lines pairs exts msgs
    local line name desc directive="" msg
    # (@) keeps the empty current word, so "PROG --flag " completes the flag's values.
    lines=(${(f)"$(PROG __complete "${(@)words[2,$CURRENT]}" 2>/dev/null)"})
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines,
    # then a ":rotini:<directive>" line (none, file, file <exts>, directory).
    for line in $lines; do
        if [[ $line == ':rotini:message '* ]]; then
            msgs+=("${line#:rotini:message }")
            continue
        fi
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
    for msg in $msgs; do
        _message -r "$msg"
    done
    (( $#pairs )) && _describe 'PROG' pairs

    case $directive in
        none) return 0 ;;
        directory) _files -/ ;;
        file) _files ;;
        "file "*) exts=(${=directive#file }); _files -g "*.(${(j:|:)exts})" ;;
    esac
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
function __PROG_raw
    set -l cur (commandline -ct)
    set -l tokens (commandline -opc)
    # Quoted so an empty current word is still passed.
    set -a tokens "$cur"
    PROG __complete $tokens[2..-1] 2>/dev/null
end

# Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines (fish
# doesn't show them), then a ":rotini:<directive>" line (none, file, file <exts>, directory).
function __PROG_load
    set -g __PROG_results
    set -g __PROG_directive ""
    for line in (__PROG_raw)
        if string match -q ':rotini:message *' -- $line
            continue
        else if string match -q ':rotini:*' -- $line
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
            set -l cur (commandline -ct)
            __fish_complete_directories $cur
            for ext in (string split ' ' -- (string replace 'file ' '' -- $__PROG_directive))
                for f in $cur*.$ext
                    test -f $f; and echo $f
                end
            end
        case '*'
            __fish_complete_path (commandline -ct)
    end
end

complete -c PROG -f
complete -c PROG -f -n '__PROG_has_results' -a '$__PROG_results'
complete -c PROG -f -n '__PROG_wants_files' -a '(__PROG_files)'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    # Pass the empty current word; before PowerShell 7.3, and with Legacy argument passing, an
    # empty argument only survives as '""'.
    if ($wordToComplete -eq '') {
        $legacy = $PSVersionTable.PSVersion -lt [version]'7.3' -or $PSNativeCommandArgumentPassing -eq 'Legacy'
        $tokens += $(if ($legacy) { '""' } else { '' })
    }
    $lines = @(PROG __complete @tokens 2>$null)
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines (not
    # shown in PowerShell), then a ":rotini:<directive>" line (none, file, file <exts>, directory).
    $lines = @($lines | Where-Object { $_ -notlike ':rotini:message *' })
    $directive = ($lines | Where-Object { $_ -like ':rotini:*' } | Select-Object -Last 1)
    $candidates = $lines | Where-Object { $_ -notlike ':rotini:*' }

    $candidates | ForEach-Object {
        $parts = $_ -split "` + "`" + `t", 2
        $text = $parts[0]
        $tip = if ($parts.Count -gt 1 -and $parts[1]) { $parts[1] } else { $text }
        [System.Management.Automation.CompletionResult]::new($text, $text, 'ParameterValue', $tip)
    }

    if ($directive) {
        $hint = $directive -replace '^:rotini:', ''
        switch -Wildcard ($hint) {
            # An empty string stops PowerShell's fallback to file names.
            'none' { if (-not $candidates) { '' }; return }
            'directory' {
                Get-ChildItem -Directory -Filter "$wordToComplete*" -ErrorAction SilentlyContinue |
                    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_.Name, $_.Name, 'ProviderContainer', $_.FullName) }
            }
            'file *' {
                Get-ChildItem -Directory -Filter "$wordToComplete*" -ErrorAction SilentlyContinue |
                    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_.Name, $_.Name, 'ProviderContainer', $_.FullName) }
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
