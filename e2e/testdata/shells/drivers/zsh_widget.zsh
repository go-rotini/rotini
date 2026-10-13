# Completes each case in a cases file the way TAB does in a live zsh: an interactive zsh -f in a
# pseudo-terminal, with compinit, the generated script sourced and no zstyle set, so zsh's own
# matching decides what is kept, as it does for a user with no matcher-list. compadd is wrapped
# to print the matches zsh keeps for each call (compadd -O), so the output is what the widget
# offers, not what the script handed it.
# Prints "== <case>", then one kept match per line.
# Usage: zsh -f zsh_widget.zsh <completion script> <cases file>
zmodload zsh/zpty zsh/zselect

setup=(
    "PS1='' PROMPT_EOL_MARK=''"
    'autoload -Uz compinit && compinit -u -D'
    "source ${(q)PWD}/$1"
    'compadd() { if (( ${@[(I)-[OAD]*]} )); then builtin compadd "$@"; return; fi; local -a kept; builtin compadd -O kept "$@"; (( $#kept )) && print -rl -- "KEPT${:-:}"${^kept}; builtin compadd "$@"; }'
    '_rotini_tab() { zle complete-word; print -r -- "DO${:-NE}"; }'
    'zle -N _rotini_tab && bindkey "^I" _rotini_tab'
)

while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    line=${line%|}
    print -r -- "== $name"
    zpty z zsh -f -i
    for cmd in $setup; do
        zpty -w z "$cmd"
    done
    zpty -w -n z "$line"$'\t'
    buf= chunk=
    integer i
    for (( i = 0; i < 200; i++ )); do
        while zpty -r -t z chunk; do
            buf+=$chunk
        done
        [[ $buf == *DONE* ]] && break
        zselect -t 5
    done
    zpty -d z
    for l in ${(f)buf//$'\r'/}; do
        if [[ $l == *KEPT:* ]]; then
            print -r -- "${l##*KEPT:}"
        fi
    done
done < $2
