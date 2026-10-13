# Completes every case in cases.txt by calling the generated _<program> (the program being each
# case's first word) with the words and CURRENT zsh sets. zsh's helpers only work inside a live
# completion widget, so _describe, _message and the path and name helpers are stand-ins that
# print what the script handed them: _describe's pairs, read as _describe reads them (with
# "# keep-order" for -V, "# nospace" for -S '' and "# ignore-case" for -M), and the helper called
# for a directive, as "<_files -g *.(yaml|yml)>". zsh_widget.zsh completes some cases in a live
# widget instead.
# Prints "== <case>", then "<value><TAB><description>" per candidate, then "! <message>" per message.
# Usage: zsh -f zsh.zsh <completion script> <cases file>
compdef() { :; }
typeset -A compstate
source $1

_describe() {
    local keep=0 pair value desc i
    [[ $1 == -V ]] && { keep=1; shift; }
    local array=$2
    shift 2
    (( keep )) && print '# keep-order'
    [[ "$*" == *"-S "* ]] && print '# nospace'
    [[ "$*" == *"-M "* ]] && print '# ignore-case'
    for pair in "${(@P)array}"; do
        # name:description. As _describe reads it, a backslash in the name escapes the
        # character after it, and the first colon not escaped ends the name.
        value= desc= i=1
        while (( i <= ${#pair} )); do
            if [[ ${pair[i]} == '\' ]]; then
                value+=${pair[i+1]}
                (( i += 2 ))
                continue
            fi
            if [[ ${pair[i]} == ':' ]]; then
                desc=${pair[i+1,-1]}
                break
            fi
            value+=${pair[i]}
            (( i++ ))
        done
        print -r -- "$value"$'\t'"${desc//\\:/:}"
    done
}
_message() { print -r -- "! ${@[-1]}"; }
for helper in _files _hosts _users _groups _command_names; do
    eval "$helper() { print -r -- \"<$helper\${*:+ \$*}>\"; }"
done

while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    line=${line%|}
    words=(${(s: :)line})
    [[ $line == *' ' ]] && words+=('')
    CURRENT=${#words}
    # Inside a quote, zsh gives the word after the quote in PREFIX and the quote in compstate.
    compstate=() PREFIX= SUFFIX=
    if [[ ${words[CURRENT]} == [\'\"]* ]]; then
        compstate[quote]=${words[CURRENT][1]}
        PREFIX=${words[CURRENT][2,-1]}
    fi
    print -r -- "== $name"
    "_${words[1]}"
done < $2
