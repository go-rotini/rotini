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
	script := tmpl
	if shell == "bash" || shell == "fish" {
		// Variable names can't hold "-", so the script's own functions and variables use an
		// identifier form of the name; the program is still registered under its real name.
		script = strings.ReplaceAll(script, "_PROG", "_"+shellIdentifier(prog))
	}
	script = strings.ReplaceAll(script, "PROG", prog)
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

// shellIdentifier returns prog in a form a shell variable name can hold, writing "_" as "_5f"
// and "-" as "_2d", so two program names never share one: my-app and my_app installed side by
// side keep their own functions.
func shellIdentifier(prog string) string {
	return strings.NewReplacer("_", "_5f", "-", "_2d").Replace(prog)
}

const bashCompletionTemplate = `# bash completion for PROG
# Completion messages show on the second TAB in bash 4.4 or later, and descriptions on the
# second TAB in bash 4.0 or later.
# Counters are set with $(( )) rather than run as (( )) commands: in bash 4.1 or later a (( ))
# that comes to 0 fails, which ends a shell running with set -e.

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
        _PROG_add_lines < <(compgen -f -- "$cur")
    fi
    _PROG_print_messages
    if (( ${#COMPREPLY[@]} )); then
        printf -- '--'
    else
        printf '%s' "${PS1@P}${COMP_LINE}"
    fi
    return 0
}

# Appends each line of standard input to COMPREPLY as one candidate, unexpanded. Lines are read
# as bytes: bash 5.2 in a UTF-8 locale reads a newline after a byte that starts a character
# but doesn't finish one as part of the line, joining two lines.
_PROG_add_lines() {
    local LC_ALL=C line k=${#COMPREPLY[@]}
    while IFS= read -r line; do
        COMPREPLY[k++]=$line
    done
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
    local v width=0 cols=${COLUMNS:-80} line k=0
    local -a out=()
    for v in "${COMPREPLY[@]}"; do
        (( ${#v} > width )) && width=${#v}
    done
    # The descriptions are walked as positional parameters, in step with the candidates.
    set -- "${__PROG_descs[@]}"
    for v in "${COMPREPLY[@]}"; do
        line=$v
        if [[ -n $1 ]]; then
            printf -v line '%-*s  (%s)' "$width" "$v" "$1"
            if (( ${#line} > cols - 1 )); then
                line="${line:0:cols-5}...)"
            fi
        fi
        out[k++]=$line
        shift
    done
    COMPREPLY=("${out[@]}")
    return 0
}

# Sets args to the words after the program up to the cursor's word, as the program will receive
# them. A word the completion word breaks split (--flag=value, host:port, user@host) is joined
# again, and each word's quotes and backslashes are removed. For the cursor's word it also sets
# lead, open and cur (see _PROG_unquote). COMP_LINE places the words; without it, COMP_WORDS is
# taken as is.
_PROG_words() {
    local i word space pos=0 last
    local -a raw=()
    for (( i = 0; i <= COMP_CWORD; i++ )); do
        word=${COMP_WORDS[i]}
        space=${COMP_LINE:pos}
        space=${space%%[![:space:]]*}
        pos=$(( pos + ${#space} ))
        if [[ ${COMP_LINE:pos:${#word}} != "$word" ]]; then
            raw=("${COMP_WORDS[@]:0:COMP_CWORD+1}")
            break
        fi
        pos=$(( pos + ${#word} ))
        last=$(( ${#raw[@]} - 1 ))
        if (( i > 1 )) && [[ -z $space && -n $joins ]] && [[ $word == ["$joins"]* || ${raw[last]} == *["$joins"] ]]; then
            raw[last]+=$word
        else
            raw+=("$word")
        fi
    done
    for (( i = 1; i < ${#raw[@]}; i++ )); do
        _PROG_unquote "${raw[i]}"
        args+=("$REPLY")
    done
    cur=${REPLY:${#lead}}
    return 0
}

# Sets REPLY to $1 with its quotes and backslashes removed, expanding nothing. Readline replaces
# only the end of the word: the text after a quote the word leaves open (open is set to that
# quote), or else after the last unquoted word break. lead is set to the unquoted text before
# that part, and cur to the part itself.
_PROG_unquote() {
    local w=$1 c q="" i=0 brk="" qlead=""
    REPLY=""
    while (( i < ${#w} )); do
        c=${w:i:1}
        if [[ $q == "'" ]]; then
            if [[ $c == "'" ]]; then q=""; else REPLY+=$c; fi
        elif [[ $q == '"' ]]; then
            if [[ $c == '"' ]]; then
                q=""
            elif [[ $c == '\' && ${w:i+1:1} == [\"\\\$\` + "`" + `] ]]; then
                i=$(( i + 1 ))
                REPLY+=${w:i:1}
            else
                REPLY+=$c
            fi
        else
            case $c in
                [\'\"])
                    q=$c
                    qlead=$REPLY
                    ;;
                \\)
                    i=$(( i + 1 ))
                    REPLY+=${w:i:1}
                    ;;
                *)
                    REPLY+=$c
                    [[ -n $joins && $joins == *"$c"* ]] && brk=$REPLY
                    ;;
            esac
        fi
        i=$(( i + 1 ))
    done
    open=$q
    lead=$brk
    [[ -n $q ]] && lead=$qlead
    return 0
}

# Sets REPLY to $1 quoted for readline to insert: inside the quote the word leaves open, or else
# with backslashes. Pieces quoted this way can be joined.
_PROG_quote() {
    REPLY=""
    [[ -n $1 ]] || return 0
    case $open in
        "'")
            REPLY=${1//\'/\'\\\'\'}
            ;;
        '"')
            REPLY=${1//\\/\\\\}
            REPLY=${REPLY//\"/\\\"}
            REPLY=${REPLY//\$/\\\$}
            REPLY=${REPLY//\` + "`" + `/\\\` + "`" + `}
            # History expansion reads ! even inside double quotes.
            REPLY=${REPLY//!/\"\'!\'\"}
            ;;
        *)
            printf -v REPLY '%q' "$1"
            ;;
    esac
    return 0
}

# Quotes each candidate in COMPREPLY as one literal word for readline to insert. Readline
# inserts the text several candidates share, so the first candidate quotes the character where
# they part in another way, and the shared text never ends inside one character's quoting.
# Candidates are read by value: bash 3.2 finds an array element by walking the array.
_PROG_quote_all() {
    local v c p rest pre="" qpre head n=${#COMPREPLY[@]} first=1 k=0
    local -a out=()
    if (( n > 1 )); then
        pre=${COMPREPLY[0]}
        for v in "${COMPREPLY[@]}"; do
            while [[ $v != "$pre"* ]]; do
                pre=${pre%?}
            done
        done
        # The pattern matches bytes, while ${#pre} and ${v:p} count characters: a byte that isn't
        # valid UTF-8 can leave pre ending inside another candidate's character.
        for v in "${COMPREPLY[@]}"; do
            while [[ ${v:0:${#pre}} != "$pre" ]]; do
                pre=${pre%?}
            done
        done
    fi
    p=${#pre}
    _PROG_quote "$pre"
    qpre=$REPLY
    for v in "${COMPREPLY[@]}"; do
        c=${v:p:1}
        head=""
        if (( first && n > 1 )) && [[ -z $open && -n $c ]]; then
            printf -v head '%q' "$c"
            if [[ $head == "$c" ]]; then
                head=""
            elif [[ $c == "'" ]]; then
                head="\"'\""
            else
                head="'$c'"
            fi
        fi
        first=0
        if [[ -n $head ]]; then
            rest=${v:p+1}
        else
            rest=${v:p}
        fi
        # The common case of _PROG_quote, without the cost of a call per candidate.
        if [[ -z $rest ]]; then
            REPLY=""
        elif [[ -z $open ]]; then
            printf -v REPLY '%q' "$rest"
        else
            _PROG_quote "$rest"
        fi
        REPLY=$qpre$head$REPLY
        # Readline closes the quote left open, unless the text already ends with that quote.
        case $open in
            "")
                # bash before 5 leaves a leading ~ bare, which would expand to a home directory.
                [[ $REPLY == "~"* ]] && REPLY="\\$REPLY"
                ;;
            "'")
                [[ $v == *"'" ]] && REPLY=${REPLY%"'\\''"}"'\\'"
                ;;
            '"')
                [[ $v == *'"' ]] && REPLY=${REPLY%'\"'}'"\"'
                [[ $v == *'!' ]] && REPLY+='"'
                ;;
        esac
        out[k++]=$REPLY
    done
    COMPREPLY=("${out[@]}")
    return 0
}

# Reads the program's output from standard input into COMPREPLY, __PROG_descs, __PROG_messages,
# options and directive. Output: candidates as "name<TAB>description", then
# ":rotini:message <text>" lines, then a ":rotini:option <word>..." line (nospace, keep-order),
# then a ":rotini:<directive>" line (none, file, file <exts>, directory, executable, user, group,
# host). Read line by line, as bytes (see _PROG_add_lines): an unquoted $(...) would also
# glob-expand each candidate.
_PROG_read_output() {
    local LC_ALL=C line value i=0
    while IFS= read -r line; do
        case "$line" in
            ":rotini:message "*) __PROG_messages+=("${line#:rotini:message }") ;;
            ":rotini:option "*) options=" ${line#:rotini:option } " ;;
            ":rotini:"*) directive="${line#:rotini:}" ;;
            *)
                # Readline replaces only the text after lead.
                value=${line%%$'\t'*}
                [[ $value == "$lead"* ]] && value=${value#"$lead"}
                COMPREPLY[i]=$value
                __PROG_descs[i]=""
                if [[ $line == *$'\t'* ]]; then
                    __PROG_descs[i]=${line#*$'\t'}
                    __PROG_described=1
                fi
                i=$(( i + 1 ))
                ;;
        esac
    done
    return 0
}

_PROG_complete() {
    local cur="" lead="" open="" directive="" options="" ext exts REPLY joins
    local -a args=()
    __PROG_messages=()
    __PROG_descs=()
    __PROG_described=0
    # The word breaks that can sit inside one word; the rest end the word or quote.
    joins=${COMP_WORDBREAKS//[[:space:]\"\'\;\|\&\(\)\<\>]/}
    # Before narrowing IFS: bash 3.2 joins an array slice into one word otherwise.
    _PROG_words
    COMPREPLY=()
    local IFS=$'\n'
    _PROG_read_output < <(PROG __complete "${args[@]}" 2>/dev/null)

    [[ $options == *" nospace "* ]] && _PROG_compopt -o nospace
    [[ $options == *" keep-order "* ]] && _PROG_compopt -o nosort
    case "$directive" in
        none)
            _PROG_compopt +o default
            ;;
        directory)
            _PROG_compopt -o filenames
            _PROG_add_lines < <(compgen -d -- "$cur")
            ;;
        file)
            _PROG_compopt -o filenames
            _PROG_add_lines < <(compgen -f -- "$cur")
            ;;
        file\ *)
            _PROG_compopt -o filenames
            exts="${directive#file }"
            _PROG_add_lines < <(compgen -d -- "$cur")
            for ext in ${exts// /$'\n'}; do
                _PROG_add_lines < <(compgen -f -X "!*.$ext" -- "$cur")
            done
            ;;
        executable)
            _PROG_compopt +o default
            _PROG_add_lines < <(compgen -c -- "$cur")
            ;;
        user)
            _PROG_compopt +o default
            _PROG_add_lines < <(compgen -u -- "$cur")
            ;;
        group)
            _PROG_compopt +o default
            _PROG_add_lines < <(compgen -g -- "$cur")
            ;;
        host)
            _PROG_compopt +o default
            _PROG_add_lines < <(compgen -A hostname -- "$cur")
            ;;
    esac
    # Readline inserts a candidate, or what several share, as given: quote each one so spaces
    # and shell characters stay part of the word. The listing (COMP_TYPE 63) shows them as
    # they are. -o filenames quotes file names, and every candidate with them, where bash has
    # compopt to set it.
    if (( ${#COMPREPLY[@]} )) && [[ $COMP_TYPE != 63 ]]; then
        if [[ $directive != file* && $directive != directory ]] || ! type compopt >/dev/null 2>&1; then
            _PROG_quote_all
        fi
    fi
    _PROG_describe
    _PROG_show_messages "$directive"
}
complete -o default -F _PROG_complete PROG
`

const zshCompletionTemplate = `#compdef PROG
# zsh completion for PROG
_PROG() {
    local -a args lines pairs exts msgs opts
    local line name desc directive="" options="" msg
    # (Q) removes the quotes and backslashes the words were typed with. Inside a quote the
    # current word is the text after it, so it is quoted again first. The empty current word is
    # passed too, so "PROG --flag " completes the flag's values.
    args=("${(@Q)words[2,CURRENT-1]}")
    if [[ -n $compstate[quote] ]]; then
        args+=("${(Q)${:-$compstate[quote]$PREFIX$SUFFIX$compstate[quote]}}")
    else
        args+=("${(Q)words[CURRENT]}")
    fi
    lines=(${(f)"$(PROG __complete "${args[@]}" 2>/dev/null)"})
    # Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines, then
    # a ":rotini:option <word>..." line (nospace, keep-order, ignore-case), then a
    # ":rotini:<directive>" line (none, file, file <exts>, directory, executable, user, group,
    # host).
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
        # _describe reads a backslash as an escape, and a colon in a name as the end of it.
        if [[ $line == *$'\t'* ]]; then
            name=${line%%$'\t'*}
            desc=${line#*$'\t'}
            pairs+=("${${name//\\/\\\\}//:/\\:}:${${desc//\\/\\\\}//:/\\:}")
        else
            pairs+=("${${line//\\/\\\\}//:/\\:}")
        fi
    done
    for msg in $msgs; do
        _message -r "$msg"
    done
    [[ $options == *" nospace "* ]] && opts+=(-S '')
    # The candidates match the typed word in any case: so must zsh, which otherwise keeps only
    # those that match it exactly.
    [[ $options == *" ignore-case "* ]] && opts+=(-M 'm:{[:lower:][:upper:]}={[:upper:][:lower:]}')
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
        # No directive: files, as the other shells fall back to.
        "") (( $#pairs )) || _files ;;
    esac
}
compdef _PROG PROG
`

const fishCompletionTemplate = `# fish completion for PROG
# Output: candidates as "name<TAB>description", then ":rotini:message <text>" lines (fish
# doesn't show them), then a ":rotini:option <word>..." line, then a ":rotini:<directive>" line
# (none, file, file <exts>, directory, executable, user, group, host). The candidates come in
# the order to show them, so they are registered with --keep-order (fish 3.1 or later). fish
# adds no space after a candidate ending in = on its own.

# Prints the candidates, or when there are none, what fish completes for the directive. One
# call per TAB, so the program runs once.
function __PROG_complete
    # The words without the quotes and backslashes they were typed with; the current word is
    # quoted so that, even empty, it is passed.
    set -l cur (string unescape -- (commandline -ct))
    set -l tokens (commandline -opc)
    set -a tokens "$cur"
    set -l results
    set -l directive ""
    for line in (PROG __complete $tokens[2..-1] 2>/dev/null)
        if string match -q ':rotini:message *' -- $line
            continue
        else if string match -q ':rotini:option *' -- $line
            continue
        else if string match -q ':rotini:*' -- $line
            set directive (string replace ':rotini:' '' -- $line)
        else
            set -a results $line
        end
    end
    if set -q results[1]
        printf '%s\n' $results
    else if test "$directive" != none
        __PROG_files "$directive" "$cur"
    end
end

# The shell's own completions for directive $1, with $2 the current word: paths, and the names
# fish's own helpers list (checked with fish 4.6).
function __PROG_files
    switch $argv[1]
        case directory
            __fish_complete_directories (commandline -ct)
        case 'file *'
            __fish_complete_directories (commandline -ct)
            for ext in (string split ' ' -- (string replace 'file ' '' -- $argv[1]))
                for f in $argv[2]*.$ext
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
complete -c PROG -f -k -a '(__PROG_complete)'
`

const powershellCompletionTemplate = `# PowerShell completion for PROG
Register-ArgumentCompleter -Native -CommandName PROG -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    # Each word as the program receives it, without the quotes it was typed with.
    $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object {
        if ($_ -is [System.Management.Automation.Language.StringConstantExpressionAst] -or
            $_ -is [System.Management.Automation.Language.ExpandableStringExpressionAst]) { $_.Value }
        elseif ($_.Extent.Text -ceq "@('--%')") { '--%' }
        else { $_.Extent.Text }
    })
    $partial = ''
    if ($wordToComplete -ne '' -and $tokens.Count) { $partial = $tokens[-1] }
    # Pass the empty current word; before PowerShell 7.3, and with Legacy argument passing, an
    # empty argument only survives as '""'.
    if ($wordToComplete -eq '') {
        $legacy = $PSVersionTable.PSVersion -lt [version]'7.3' -or $PSNativeCommandArgumentPassing -eq 'Legacy'
        $tokens += $(if ($legacy) { '""' } else { '' })
    }
    # Passed as an array, not splatted: splatting drops a --% word.
    $lines = @(PROG __complete $tokens 2>$null)
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
        # PowerShell inserts the text as typed, so quote one with spaces, wildcards, shell
        # characters or a leading ~ (a home directory to native commands). PowerShell splits a
        # bare argument that starts with a dash (- or U+2013 to U+2015) at a ., and reads one
        # ending in : as a parameter awaiting its value, so quote a dashed value holding either.
        # It drops the argument --% however it is quoted, and passes it only from an array. It
        # reads the typographic quotes U+2018 to U+201E as quotes too, and inside single quotes
        # doubles each form of single quote.
        $insert = if ($text -ceq '--%') { "@('--%')" }
            elseif ($text -match '^~|^[-\u2013-\u2015].*[.:]|[\s''"$&|;<>(){}@#,*?\[\]` + "`" + `\u2018-\u201E]') { "'" + ($text -replace '[''\u2018-\u201B]', '$0$0') + "'" }
            else { $text }
        [System.Management.Automation.CompletionResult]::new($insert, $text, 'ParameterValue', $tip)
    }

    if ($directive) {
        $hint = $directive -replace '^:rotini:', ''
        # The names for executable, user and group; PowerShell has no host completer, and lists
        # users and groups on Windows only.
        $names = $null
        switch -Wildcard ($hint) {
            # An empty string stops PowerShell's fallback to file names.
            'none' { if (-not $candidates) { '' }; return }
            # PowerShell's own file name completion keeps the typed folder and quotes the names.
            'directory' {
                [System.Management.Automation.CompletionCompleters]::CompleteFilename($wordToComplete) |
                    Where-Object { $_.ResultType -eq 'ProviderContainer' }
            }
            'file *' {
                $exts = ($hint -replace '^file ', '') -split ' '
                [System.Management.Automation.CompletionCompleters]::CompleteFilename($wordToComplete) |
                    Where-Object { $_.ResultType -eq 'ProviderContainer' -or $exts -contains [System.IO.Path]::GetExtension($_.ToolTip).TrimStart('.') }
            }
            'file' {
                [System.Management.Automation.CompletionCompleters]::CompleteFilename($wordToComplete)
            }
            'executable' {
                $names = @(Get-Command -CommandType Application -Name "$partial*" -ErrorAction SilentlyContinue |
                    ForEach-Object { $_.Name } | Sort-Object -Unique)
            }
            'user' {
                $names = @()
                if (Get-Command Get-LocalUser -ErrorAction SilentlyContinue) {
                    $names = @(Get-LocalUser -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "$partial*" } | ForEach-Object { $_.Name })
                }
            }
            'group' {
                $names = @()
                if (Get-Command Get-LocalGroup -ErrorAction SilentlyContinue) {
                    $names = @(Get-LocalGroup -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "$partial*" } | ForEach-Object { $_.Name })
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
    let typed = ($place.command | skip 1)
    let partial = ($typed | last)
    # Nushell gives each word as typed. The program gets it without the quotes it was typed in,
    # including a quote the last word leaves open.
    let unquote = {|w|
        if ($w =~ "^r#+'") {
            let closed = ($w | parse --regex "^r(?<h>#+)'(?<v>.*)'(?P=h)$")
            if ($closed | is-not-empty) { $closed.0.v } else { $w | str replace --regex "^r#+'" '' }
        } else if ($w =~ "^['` + "`" + `]") {
            $w | str replace --regex "^(['` + "`" + `])(.*?)(?:\\1)?$" '$2'
        } else if ($w | str starts-with '"') {
            $w | str replace --regex '^"((?:[^"\\]|\\.)*)"?$' '$1' | str replace --all --regex '\\(["\\])' '$1'
        } else {
            $w
        }
    }
    let words = ($typed | each {|w| do $unquote $w })
    let lines = (^PROG __complete ...$words | complete | get stdout | lines)
    let options = ($lines | where ($it | str starts-with ':rotini:option ') | each { str substring 15.. | split row ' ' } | flatten)
    let directive = ($lines | where ($it | str starts-with ':rotini:') and not ($it | str starts-with ':rotini:message ') and not ($it | str starts-with ':rotini:option ') | each { str substring 8.. } | append '' | first)
    let space = not ('nospace' in $options)
    # Nushell inserts a value as given: one that isn't a plain word goes in single quotes, or
    # in a raw string when it holds a single quote, so it stays one literal argument. Nushell
    # reads a bare null as no value, and a bare =, += and the like as an assignment.
    let quote = {|v|
        if ($v =~ '^[\w.:/=+-]+$') and not ($v | str starts-with '~') and $v != 'null' and not ($v =~ '^(\+\+|[+\-/*])?=$') {
            $v
        } else if not ($v | str contains "'") {
            $"'($v)'"
        } else {
            mut hashes = '#'
            while ($v | str contains $"'($hashes)") { $hashes = $hashes + '#' }
            $"r($hashes)'($v)'($hashes)"
        }
    }
    mut found = ($lines | where not ($it | str starts-with ':rotini:') | each {|line|
        let parts = ($line | split row --number 2 "\t")
        if ($parts | length) > 1 and ($parts.1 | is-not-empty) {
            {value: (do $quote $parts.0), description: $parts.1, append_whitespace: $space}
        } else {
            {value: (do $quote $parts.0), append_whitespace: $space}
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
                # Nushell quotes a path it completes; the name is read inside the quotes.
                let bare = {|p| $p | str replace --regex "^[\"'` + "`" + `](.*)[\"'` + "`" + `]$" '$1' }
                let paths = ($partial | commandline complete --type path | where {|p| (do $bare $p | str ends-with '/') or ((do $bare $p | path parse | get extension) in $exts) })
                $found = ($found | append ($paths | each {|p| {value: $p, append_whitespace: (not (do $bare $p | str ends-with '/'))} }))
            }
        }
        '' => {
            if ($found | is-empty) { return null }
        }
    }
    {completions: $found, options: {sort: (not ('keep-order' in $options))}}
}

# Bare true and false are read as booleans, which reach the program as the same words.
@complete "nu-complete PROG"
extern PROG [...args: oneof<string, bool>]
`
