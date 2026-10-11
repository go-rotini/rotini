# Completes every case in cases.txt the way bash's TAB does: source the generated script, set the
# COMP_* variables bash sets, and call the function `complete -p app` names. The first TAB
# (COMP_TYPE 9) gives the values inserted; the second (COMP_TYPE 63) gives the listing, whose
# "value  (description)" form supplies the descriptions, and prints any messages.
# Prints "== <case>", then "<value><TAB><description>" per candidate, then "! <message>" per message.
# Usage: bash bash.sh <completion script> <cases file>
PS1=''
source "$1"
fn=$(complete -p app | sed -n 's/.* -F \([^ ]*\) .*/\1/p')
out=$(mktemp)
while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    line=${line%|}
    read -ra COMP_WORDS <<<"$line"
    [[ $line == *' ' ]] && COMP_WORDS+=("")
    COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
    COMP_LINE=$line
    COMP_POINT=${#line}
    echo "== $name"

    COMP_TYPE=9
    COMPREPLY=()
    "$fn" >/dev/null
    values=("${COMPREPLY[@]}")

    COMP_TYPE=63
    COMPREPLY=()
    "$fn" >"$out"
    listed=("${COMPREPLY[@]}")

    for i in "${!values[@]}"; do
        value=${values[i]} desc=
        if ((${#listed[@]} == ${#values[@]})); then
            rest=${listed[i]#"$value"}
            rest=${rest#"${rest%%[! ]*}"}
            [[ $rest == '('*')' ]] && desc=${rest:1:${#rest}-2}
        fi
        printf '%s\t%s\n' "$value" "$desc"
    done
    # The listing's messages: a blank line, the messages, then "--" or the prompt and line again.
    sed -e '1{/^$/d;}' -e '$d' "$out" | while IFS= read -r msg; do printf '! %s\n' "$msg"; done
done <"$2"
rm -f "$out"
