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
		// Every script's comments start with "#"; the first line stays first (zsh's #compdef).
		first, rest, _ := strings.Cut(script, "\n")
		script = first + "\n# Set " + messagesEnv + "=off to hide completion messages.\n" + rest
	}
	return script, nil
}

const bashCompletionTemplate = `# bash completion for PROG
# Candidates arrive as "name<TAB>description"; bash cannot render descriptions,
# so everything from the first tab is stripped.
#
# A final ":rotini:<directive>" line is the spec's declarative completion hint
# for the value being typed — file, directory, or none. It is handled here rather
# than offered as a candidate.
#
# A ":rotini:message <text>" line is a message to show, never a candidate. bash 4.4 and
# later print it below the prompt and redraw the line; older bash, including macOS's
# /bin/bash 3.2, skips it.

# compopt does not exist in bash 3.2, which is /bin/bash on every macOS, and where it does exist
# it fails when called outside a live completion. Either failure must stay inside this
# function: under 'set -e' a failing compopt would end the whole shell before any 'return 0'
# could run, so its status is absorbed on the spot. The directives still work without it; only
# bash's own file fallback stays on for a "none" value, which is the most a 3.2 user can get.
_PROG_compopt() {
    if type compopt >/dev/null 2>&1; then
        compopt "$@" 2>/dev/null || true
    fi
    return 0
}

# _PROG_show_messages prints the messages the last completion collected, below the prompt,
# then has readline redraw the line being edited. It needs a terminal and bash 4.4 or later.
_PROG_show_messages() {
    (( ${#__PROG_messages[@]} )) || return 0
    (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )) || return 0
    { : >/dev/tty; } 2>/dev/null || return 0
    printf '\n%s' "${__PROG_messages[@]}" >/dev/tty
    printf '\n' >/dev/tty
    bind '"\e[0n": redraw-current-line' 2>/dev/null || true
    printf '\e[5n' >/dev/tty
    return 0
}

_PROG_complete() {
    local args line directive="" ext exts
    __PROG_messages=()
    # Slice COMP_WORDS BEFORE narrowing IFS. bash 3.2 — which is /bin/bash on every macOS —
    # collapses "${array[@]:offset:length}" into ONE IFS-joined element whenever IFS does not
    # contain a space, so with IFS=$'\n' set first this produced a single argument and every
    # completion past the first word silently offered nothing.
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    COMPREPLY=()
    # Now narrow it: the binary separates candidates by newline, and a candidate (a file name,
    # a summary) may contain spaces.
    local IFS=$'\n'
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        case "$line" in
            ":rotini:message "*) __PROG_messages+=("${line#:rotini:message }") ;;
            ":rotini:"*) directive="${line#:rotini:}" ;;
            *) COMPREPLY+=("${line%%$'\t'*}") ;;
        esac
    done
    _PROG_show_messages

    case "$directive" in
        none)
            # An opaque value: suppress bash's default file fallback entirely.
            _PROG_compopt +o default
            ;;
        directory)
            _PROG_compopt -o dirnames
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
            # IFS is narrowed to newline above, so the space-separated list is re-separated
            # by newline; a plain $exts would arrive as ONE extension, "yaml yml".
            for ext in ${exts// /$'\n'}; do
                COMPREPLY+=($(compgen -f -X "!*.$ext" -- "${COMP_WORDS[$COMP_CWORD]}")) || true
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
    local -a lines pairs exts msgs
    local line name desc directive="" msg
    # "${(@)words[...]}" — not ${words[...]}. An unquoted slice DROPS the empty element,
    # and the current word is empty in the commonest case of all: the cursor sitting after
    # "PROG hash --algorithm ". The binary would then be asked to complete "--algorithm"
    # itself and would offer the flag again instead of its values. (@) inside quotes keeps
    # every element, empties included.
    lines=(${(f)"$(PROG __complete "${(@)words[2,$CURRENT]}" 2>/dev/null)"})
    for line in $lines; do
        # A ":rotini:message <text>" line is a message to show, never a candidate.
        if [[ $line == ':rotini:message '* ]]; then
            msgs+=("${line#:rotini:message }")
            continue
        fi
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
    for msg in $msgs; do
        _message -r "$msg"
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
    # An unquoted (commandline -ct) yields NO element when the current token is empty, which
    # is exactly the case that matters — the cursor after "PROG hash --algorithm ". The
    # binary would see only the flag and offer it again. Quoting forces one element, empty
    # or not, so the trailing word always reaches __complete.
    set -l cur (commandline -ct)
    set -l tokens (commandline -opc)
    set -a tokens "$cur"
    PROG __complete $tokens[2..-1] 2>/dev/null
end

# Split the binary's output into candidates and the trailing ":rotini:<directive>"
# line, which carries the spec's declarative hint for the value being typed.
function __PROG_load
    set -g __PROG_results
    set -g __PROG_directive ""
    for line in (__PROG_raw)
        # A ":rotini:message <text>" line is a message, which fish has no place to show.
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

# fish's own file completion stays off: when it applies, __PROG_files supplies the paths,
# and a "none" hint must offer nothing at all.
complete -c PROG -f

# Offer the binary's candidates when it has any; otherwise the paths the hint asks
# for. Candidates arrive as "name<TAB>description" — fish renders that natively.
complete -c PROG -f -n '__PROG_has_results' -a '$__PROG_results'
complete -c PROG -f -n '__PROG_wants_files' -a '(__PROG_files)'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
    # The cursor after a space completes a NEW, empty word, so an empty argument is sent. Before
    # PowerShell 7.3 (Windows PowerShell 5.1 included), and in Legacy argument passing, an empty
    # argument is silently dropped on its way to a program; '""' is what arrives as one there.
    if ($wordToComplete -eq '') {
        $legacy = $PSVersionTable.PSVersion -lt [version]'7.3' -or $PSNativeCommandArgumentPassing -eq 'Legacy'
        $tokens += $(if ($legacy) { '""' } else { '' })
    }
    $lines = @(PROG __complete @tokens 2>$null)
    # A ":rotini:message <text>" line is a message, which PowerShell has no place to show. A
    # final ":rotini:<directive>" line is the spec's declarative hint for the value being typed;
    # everything else is a candidate.
    $lines = @($lines | Where-Object { $_ -notlike ':rotini:message *' })
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
