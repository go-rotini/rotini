package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// completionEnvs names the environment variables the end user sets to switch completion:
// messages_env and descriptions_env, each "" when not declared.
type completionEnvs struct {
	messages     string // hides completion messages; named only when messages are on
	descriptions string // hides candidate descriptions
}

// completionScriptEnvs returns the completion feature's switch variables, or none when the
// feature is off.
func completionScriptEnvs(conf *Conf) completionEnvs {
	mode, messages := completionMessages(conf)
	if mode == "" {
		messages = ""
	}
	var descriptions string
	if conf != nil && featureEnabled(conf, "completion") {
		if f := conf.Generate.featureOf("completion"); f != nil {
			descriptions = f.DescriptionsEnv
		}
	}
	return completionEnvs{messages: messages, descriptions: descriptions}
}

// completionEnvRows returns the root man page's ENVIRONMENT rows for the completion switch
// variables the conf declares.
func completionEnvRows(conf *Conf) []templateDocEnvRow {
	envs := completionScriptEnvs(conf)
	var rows []templateDocEnvRow
	if envs.messages != "" {
		rows = append(rows, templateDocEnvRow{Var: envs.messages, Summary: "set to 0, false or off to hide completion messages"})
	}
	if envs.descriptions != "" {
		rows = append(rows, templateDocEnvRow{Var: envs.descriptions, Summary: "set to 0, false or off to hide completion descriptions"})
	}
	return rows
}

// completionScript returns the completion script for prog in shell (bash, zsh, fish, powershell
// or nushell). Each script delegates to the binary's hidden __complete command, so completions
// track the live command tree. The completion feature renders one script per shell at
// generate time. The script's header names the variables in envs for the users who install it.
func completionScript(prog, shell string, envs completionEnvs) (string, error) {
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
	case "nushell":
		tmpl = nushellCompletionTemplate
	case "":
		return "", errors.New("a shell is required (bash, zsh, fish, powershell, or nushell)")
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell, nushell)", shell)
	}
	script := strings.ReplaceAll(tmpl, "PROG", prog)
	var header string
	if envs.messages != "" {
		header += "# Set " + envs.messages + "=off to hide completion messages.\n"
	}
	if envs.descriptions != "" {
		header += "# Set " + envs.descriptions + "=off to hide completion descriptions.\n"
	}
	if header != "" {
		title := " completion for " + prog + "\n"
		head, rest, _ := strings.Cut(script, title)
		script = head + title + header + rest
	}
	return script, nil
}

const bashCompletionTemplate = `# bash completion for PROG
# Completion messages show on the second TAB in bash 4.4 or later, and descriptions on the
# second TAB in bash 4.0 or later.

# compopt is missing in bash 3.2 (macOS's /bin/bash) and fails outside a completion; bash
# before 4.4 rejects -o nosort.
_PROG_compopt() {
    if type compopt >/dev/null 2>&1; then
        compopt "$@" 2>/dev/null || true
    fi
    return 0
}

# Prints messages on the second TAB (COMP_TYPE 63), then "--" before the candidates bash
# lists, or the prompt again when there are none. $1 is the directive; only a file directive,
# or none at all, falls back to files.
_PROG_show_messages() {
    (( ${#__PROG_messages[@]} )) || return 0
    [[ $COMP_TYPE == 63 ]] || return 0
    _PROG_messages_supported || return 0
    if (( ! ${#COMPREPLY[@]} )) && [[ -z $1 || $1 == file* ]]; then
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

# On the second TAB (COMP_TYPE 63), lists several candidates as "value  (description)",
# padded to the longest value and cut to the terminal's width. The first TAB inserts the bare
# value. Nothing is formatted once file names were added.
_PROG_describe() {
    [[ $COMP_TYPE == 63 ]] || return 0
    (( __PROG_described && ${#COMPREPLY[@]} > 1 && ${#COMPREPLY[@]} == ${#__PROG_descs[@]} )) || return 0
    local i width=0 cols=${COLUMNS:-80} line
    for i in "${!COMPREPLY[@]}"; do
        (( ${#COMPREPLY[i]} > width )) && width=${#COMPREPLY[i]}
    done
    for i in "${!COMPREPLY[@]}"; do
        [[ -n ${__PROG_descs[i]} ]] || continue
        line=$(printf '%-*s  (%s)' "$width" "${COMPREPLY[i]}" "${__PROG_descs[i]}")
        if (( ${#line} > cols - 1 )); then
            line="${line:0:cols-5}...)"
        fi
        COMPREPLY[i]=$line
    done
    return 0
}

_PROG_complete() {
    local args line cur directive="" options="" ext exts
    __PROG_messages=()
    __PROG_descs=()
    __PROG_described=0
    # Slice before narrowing IFS: bash 3.2 joins the slice into one word otherwise.
    args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    cur="${COMP_WORDS[$COMP_CWORD]}"
    COMPREPLY=()
    local IFS=$'\n'
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines, then
    # a ":rotini:option <word>..." line (nospace, keep-order), then a ":rotini:<directive>"
    # line (none, file, file <exts>, directory, executable, user, group, host).
    for line in $(PROG __complete "${args[@]}" 2>/dev/null); do
        case "$line" in
            ":rotini:message "*) __PROG_messages+=("${line#:rotini:message }") ;;
            ":rotini:option "*) options=" ${line#:rotini:option } " ;;
            ":rotini:"*) directive="${line#:rotini:}" ;;
            *)
                COMPREPLY+=("${line%%$'\t'*}")
                if [[ $line == *$'\t'* ]]; then
                    __PROG_descs+=("${line#*$'\t'}")
                    __PROG_described=1
                else
                    __PROG_descs+=("")
                fi
                ;;
        esac
    done

    [[ $options == *" nospace "* ]] && _PROG_compopt -o nospace
    [[ $options == *" keep-order "* ]] && _PROG_compopt -o nosort
    case "$directive" in
        none)
            _PROG_compopt +o default
            ;;
        directory)
            _PROG_compopt -o filenames
            COMPREPLY+=($(compgen -d -- "$cur")) || true
            ;;
        file)
            _PROG_compopt -o filenames
            COMPREPLY+=($(compgen -f -- "$cur")) || true
            ;;
        file\ *)
            _PROG_compopt -o filenames
            exts="${directive#file }"
            COMPREPLY+=($(compgen -d -- "$cur")) || true
            for ext in ${exts// /$'\n'}; do
                COMPREPLY+=($(compgen -f -X "!*.$ext" -- "$cur")) || true
            done
            ;;
        executable)
            _PROG_compopt +o default
            COMPREPLY+=($(compgen -c -- "$cur")) || true
            ;;
        user)
            _PROG_compopt +o default
            COMPREPLY+=($(compgen -u -- "$cur")) || true
            ;;
        group)
            _PROG_compopt +o default
            COMPREPLY+=($(compgen -g -- "$cur")) || true
            ;;
        host)
            _PROG_compopt +o default
            COMPREPLY+=($(compgen -A hostname -- "$cur")) || true
            ;;
    esac
    _PROG_describe
    _PROG_show_messages "$directive"
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# zsh completion for PROG
_PROG() {
    local -a lines pairs exts msgs opts
    local line name desc directive="" options="" msg
    # (@) keeps the empty current word, so "PROG --flag " completes the flag's values.
    lines=(${(f)"$(PROG __complete "${(@)words[2,$CURRENT]}" 2>/dev/null)"})
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines, then
    # a ":rotini:option <word>..." line (nospace, keep-order), then a ":rotini:<directive>"
    # line (none, file, file <exts>, directory, executable, user, group, host).
    for line in $lines; do
        if [[ $line == ':rotini:message '* ]]; then
            msgs+=("${line#:rotini:message }")
            continue
        fi
        if [[ $line == ':rotini:option '* ]]; then
            options=" ${line#:rotini:option } "
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
    [[ $options == *" nospace "* ]] && opts+=(-S '')
    if (( $#pairs )); then
        if [[ $options == *" keep-order "* ]]; then
            _describe -V 'PROG' pairs "${opts[@]}"
        else
            _describe 'PROG' pairs "${opts[@]}"
        fi
    fi

    case $directive in
        none) return 0 ;;
        directory) _files -/ ;;
        file) _files ;;
        "file "*) exts=(${=directive#file }); _files -g "*.(${(j:|:)exts})" ;;
        executable) _command_names -e ;;
        user) _users ;;
        group) _groups ;;
        host) _hosts ;;
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
# doesn't show them), then a ":rotini:option <word>..." line, then a ":rotini:<directive>" line
# (none, file, file <exts>, directory, executable, user, group, host). The candidates come in
# the order to show them, so they are registered with --keep-order (fish 3.1 or later). fish
# adds no space after a candidate ending in = on its own.
function __PROG_load
    set -g __PROG_results
    set -g __PROG_directive ""
    for line in (__PROG_raw)
        if string match -q ':rotini:message *' -- $line
            continue
        else if string match -q ':rotini:option *' -- $line
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

# The shell's own completions for a directive: paths, and the names fish's own helpers list
# (checked with fish 4.6).
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
        case executable
            __fish_complete_command
        case user
            __fish_complete_users
        case group
            __fish_complete_groups
        case host
            __fish_print_hostnames
        case '*'
            __fish_complete_path (commandline -ct)
    end
end

complete -c PROG -f
complete -c PROG -f -k -n '__PROG_has_results' -a '$__PROG_results'
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
    # shown in PowerShell), then a ":rotini:option <word>..." line, then a ":rotini:<directive>"
    # line (none, file, file <exts>, directory, executable, user, group, host). PowerShell shows
    # the candidates in the order given and adds no space after one.
    $lines = @($lines | Where-Object { $_ -notlike ':rotini:message *' -and $_ -notlike ':rotini:option *' })
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
        # The names for executable, user and group; PowerShell has no host completer, and lists
        # users and groups on Windows only.
        $names = $null
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
            'executable' {
                $names = @(Get-Command -CommandType Application -Name "$wordToComplete*" -ErrorAction SilentlyContinue |
                    ForEach-Object { $_.Name } | Sort-Object -Unique)
            }
            'user' {
                $names = @()
                if (Get-Command Get-LocalUser -ErrorAction SilentlyContinue) {
                    $names = @(Get-LocalUser -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "$wordToComplete*" } | ForEach-Object { $_.Name })
                }
            }
            'group' {
                $names = @()
                if (Get-Command Get-LocalGroup -ErrorAction SilentlyContinue) {
                    $names = @(Get-LocalGroup -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "$wordToComplete*" } | ForEach-Object { $_.Name })
                }
            }
            'host' { $names = @() }
        }
        if ($null -ne $names) {
            $names | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
            if (-not $names -and -not $candidates) { '' }
        }
    }
}
`

const nushellCompletionTemplate = `# Nushell completion for PROG
# Needs Nushell 0.116 or later. Nushell runs the files in its autoload directory at startup:
#   mkdir ($nu.user-autoload-dirs | first)
#   PROG completion nushell | save -f ($nu.user-autoload-dirs | first | path join PROG.nu)

# Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines (Nushell
# doesn't show them), then a ":rotini:option <word>..." line (nospace, keep-order), then a
# ":rotini:<directive>" line (none, file, file <exts>, directory, executable, user, group,
# host). Nushell has no completer for program, user, group or host names, so those offer
# nothing.
def "nu-complete PROG" [place: record] {
    let words = ($place.command | skip 1)
    let partial = ($words | last)
    let lines = (^PROG __complete ...$words | complete | get stdout | lines)
    let options = ($lines | where ($it | str starts-with ':rotini:option ') | each { str substring 15.. | split row ' ' } | flatten)
    let directive = ($lines | where ($it | str starts-with ':rotini:') and not ($it | str starts-with ':rotini:message ') and not ($it | str starts-with ':rotini:option ') | each { str substring 8.. } | append '' | first)
    let space = not ('nospace' in $options)
    mut found = ($lines | where not ($it | str starts-with ':rotini:') | each {|line|
        let parts = ($line | split row --number 2 "\t")
        if ($parts | length) > 1 and ($parts.1 | is-not-empty) {
            {value: $parts.0, description: $parts.1, append_whitespace: $space}
        } else {
            {value: $parts.0, append_whitespace: $space}
        }
    })
    let words = ($directive | split row ' ')
    match $words.0 {
        'directory' => {
            $found = ($found | append ($partial | commandline complete --type directory | each {|p| {value: $p, append_whitespace: false} }))
        }
        'file' => {
            let exts = ($words | skip 1)
            if ($exts | is-empty) {
                if ($found | is-empty) { return null }
                $found = ($found | append ($partial | commandline complete --type path | each {|p| {value: $p, append_whitespace: false} }))
            } else {
                let paths = ($partial | commandline complete --type path | where {|p| ($p | str ends-with '/') or (($p | path parse | get extension) in $exts) })
                $found = ($found | append ($paths | each {|p| {value: $p, append_whitespace: (not ($p | str ends-with '/'))} }))
            }
        }
        '' => {
            if ($found | is-empty) { return null }
        }
    }
    {completions: $found, options: {sort: (not ('keep-order' in $options))}}
}

@complete "nu-complete PROG"
extern PROG [...args: string]
`
