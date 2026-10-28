# Completes every case in cases.txt by calling the generated _<program> (the program being each
# case's first word, by its last part) with the words and CURRENT zsh sets. zsh's helpers only
# work inside a live completion widget, so _describe, _wanted, compadd, compset, _message and the
# path and name helpers are stand-ins that print what the script handed them: _describe's pairs,
# read as _describe reads them, and compadd's matches with the descriptions from their display
# strings (with "# keep-order" for _describe -V or _wanted -V, "# nospace" for -S '' and
# "# ignore-case" for -M), and the helper called for a directive, as
# "<_files -g *.(yaml|yml)>", followed by "after <text>" when compset kept the start of the word.
# zsh_widget.zsh completes some cases in a live widget instead.
# Prints "== <case>", then "<value><TAB><description>" per candidate, then "! <message>" per message.
# Usage: zsh -f zsh.zsh <completion script> <cases file>
compdef() { :; }
typeset -A compstate
source $1

_describe() {
    local keep=0 pair value desc i c part
    [[ $1 == -V ]] && { keep=1; shift; }
    local array=$2
    shift 2
    (( keep )) && print '# keep-order'
    [[ "$*" == *"-S "* ]] && print '# nospace'
    [[ "$*" == *"-M "* ]] && print '# ignore-case'
    for pair in "${(@P)array}"; do
        # name:description. As _describe reads it, a backslash escapes the character after it,
        # and the first colon not escaped ends the name.
        value= desc= i=1 part=name
        while (( i <= ${#pair} )); do
            c=${pair[i]}
            if [[ $c == '\' ]]; then
                c=${pair[i+1]}
                (( i++ ))
            elif [[ $part == name && $c == ':' ]]; then
                part=desc
                (( i++ ))
                continue
            fi
            if [[ $part == name ]]; then value+=$c; else desc+=$c; fi
            (( i++ ))
        done
        print -r -- "$value"$'\t'"$desc"
    done
}
# _wanted [-V] tag expl descr command...: runs the command.
_wanted() {
    [[ $1 == -V ]] && { print '# keep-order'; shift; }
    shift 3
    "$@"
}
# compadd [-S ''] [-M spec] [-l] [-d display] -a names: a display string is the name, padded,
# then "  -- " and the description.
compadd() {
    local __n __d i d
    while (( $# )); do
        case $1 in
            -S) [[ -z $2 ]] && print '# nospace'; shift 2 ;;
            -M) print '# ignore-case'; shift 2 ;;
            -d) __d=$2; shift 2 ;;
            -a) __n=$2; shift 2 ;;
            *) shift ;;
        esac
    done
    for (( i = 1; i <= ${#${(P)__n}}; i++ )); do
        d=
        if [[ -n $__d ]]; then
            d=${${(P)__d}[i]#"${${(P)__n}[i]}"}
            d=${d#"${d%%[^ ]*}"}
            d=${d#-- }
        fi
        print -r -- "${${(P)__n}[i]}"$'\t'"$d"
    done
}
# compset -P [n] pattern: moves the start of PREFIX the pattern matches (the shortest with n, the
# longest without) to IPREFIX.
compset() {
    local n=0 m
    shift
    [[ $1 == <-> ]] && { n=$1; shift; }
    if (( n )); then m=${(M)PREFIX#${~1}}; else m=${(M)PREFIX##${~1}}; fi
    [[ -n $m ]] || return 1
    IPREFIX+=$m
    PREFIX=${PREFIX:${#m}}
}
_message() { print -r -- "! ${@[-1]}"; }
for helper in _files _path_files _hosts _users _groups _command_names; do
    eval "$helper() { print -r -- \"<$helper\${*:+ \$*}>\${IPREFIX:+ after \$IPREFIX}\"; }"
done

while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    line=${line%|}
    words=(${(s: :)line})
    [[ $line == *' ' ]] && words+=('')
    CURRENT=${#words}
    # Inside a quote, zsh gives the word after the quote in PREFIX and the quote in compstate.
    compstate=() PREFIX=${words[CURRENT]} SUFFIX= IPREFIX=
    if [[ ${words[CURRENT]} == [\'\"]* ]]; then
        compstate[quote]=${words[CURRENT][1]}
        PREFIX=${words[CURRENT][2,-1]}
    fi
    print -r -- "== $name"
    "_${words[1]:t}"
done < $2
