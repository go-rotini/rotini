# Loads the generated script the two ways a user does, each in a new interactive zsh -f in a
# pseudo-terminal: sourced without compinit, which must fail with one line on standard error,
# and installed as _<program> in a directory on $fpath, whose first TAB in a new shell must
# already complete. Each case is a cases-file line, typed with a TAB in its own new shell.
# Prints "== sourced without compinit", the status and standard error, then "== <case>" and the
# line as the first TAB left it, for each case.
# Usage: zsh -f zsh_load.zsh <completion script> <program> <cases file>
zmodload zsh/zpty zsh/zselect

# Starts a new interactive zsh, runs the command $1 and, when $2 is given, types it and a TAB.
# Prints what the terminal showed until the line "DONE" or, after a TAB, the buffer line.
run() {
    local buf= chunk= want=$'*DONE\n*'
    integer i
    zpty z zsh -f -i
    zpty -w z "PS1='' PROMPT_EOL_MARK=''"
    zpty -w z "$1"
    if [[ -n $2 ]]; then
        want=$'*BUFFER:*\n*'
        zpty -w -n z "$2"$'\t'
    else
        zpty -w z 'print -r -- DO${:-NE}'
    fi
    for (( i = 0; i < 200; i++ )); do
        while zpty -r -t z chunk; do
            buf+=$chunk
        done
        [[ $buf == $~want ]] && break
        zselect -t 5
    done
    zpty -d z
    print -r -- "${buf//$'\r'/}"
}

print -r -- "== sourced without compinit"
run "source $1 2>load-err.txt; print -r -- status=\$?; print -r -- stderr: \"\$(<load-err.txt)\"" |
    sed -n -e '/^status=/p' -e '/^stderr: /p'

mkdir -p load-fpath
cp $1 load-fpath/_$2
# A widget for TAB that prints the buffer as complete-word left it.
setup="fpath=(\$PWD/load-fpath \$fpath); autoload -Uz compinit && compinit -u -D; _tab() { zle complete-word; print -r -- BUF\${:-FER}:\$BUFFER; }; zle -N _tab; bindkey '^I' _tab"
while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    print -r -- "== $name"
    run "$setup" "${line%|}" | sed -n 's/^.*BUFFER://p'
done < $3
rm -rf load-fpath load-err.txt
