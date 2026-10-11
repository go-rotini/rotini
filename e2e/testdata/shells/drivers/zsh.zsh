# Completes every case in cases.txt by calling the generated _app with the words and CURRENT zsh
# sets. zsh's helpers only work inside a live completion widget, so _describe, _message and the
# path and name helpers are stand-ins that print what the script handed them: _describe's pairs
# (with "# keep-order" for -V and "# nospace" for -S ''), and the helper called for a directive,
# as "<_files -g *.(yaml|yml)>".
# Prints "== <case>", then "<value><TAB><description>" per candidate, then "! <message>" per message.
# Usage: zsh -f zsh.zsh <completion script> <cases file>
compdef() { :; }
source $1

_describe() {
    local keep=0 pair value desc i
    [[ $1 == -V ]] && { keep=1; shift; }
    local array=$2
    shift 2
    (( keep )) && print '# keep-order'
    [[ "$*" == *"-S "* ]] && print '# nospace'
    for pair in "${(@P)array}"; do
        # name:description, each with its colons escaped as \:.
        value=$pair desc= i=1
        while (( i <= ${#pair} )); do
            if [[ ${pair[i]} == '\' ]]; then
                (( i += 2 ))
                continue
            fi
            if [[ ${pair[i]} == ':' ]]; then
                value=${pair[1,i-1]} desc=${pair[i+1,-1]}
                break
            fi
            (( i++ ))
        done
        print -r -- "${value//\\:/:}"$'\t'"${desc//\\:/:}"
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
    print -r -- "== $name"
    _app
done < $2
