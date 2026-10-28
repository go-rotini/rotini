# Completes every case in cases.txt the way bash's TAB does: source the generated script, set the
# COMP_* variables bash sets, and call the function `complete -p <program>` names, the program
# being each case's first word. bash 4 and later split COMP_WORDS at the word breaks = : and @ as
# well as at spaces; bash 3.2 splits at spaces only. The first TAB (COMP_TYPE 9) gives the values
# inserted, quoted as readline puts them on the line; the second (COMP_TYPE 63) gives the
# listing, whose "value  (description)" form, against the bare listing with descriptions off,
# supplies the descriptions, and prints any messages. bash 3.2 sets no COMP_TYPE: there the
# values are the one answer both TABs get, with descriptions off.
# Prints "== <case>", then "<value><TAB><description>" per candidate, then "! <message>" per message.
# Usage: bash bash.sh <completion script> <cases file>
PS1=''
source "$1"
out=$(mktemp)
while IFS=$'\t' read -r name line _; do
    [[ -z $name || $name == \#* ]] && continue
    line=${line%|}
    read -ra spaced <<<"$line"
    [[ $line == *' ' ]] && spaced+=("")
    COMP_WORDS=()
    for word in "${spaced[@]}"; do
        if ((BASH_VERSINFO[0] < 4)) || [[ -z $word || $word == \'* || $word == \"* ]]; then
            COMP_WORDS+=("$word")
            continue
        fi
        # A run of break characters is a word of its own.
        while [[ $word =~ ^([^=:@]*)([=:@]+)(.*)$ ]]; do
            [[ -n ${BASH_REMATCH[1]} ]] && COMP_WORDS+=("${BASH_REMATCH[1]}")
            COMP_WORDS+=("${BASH_REMATCH[2]}")
            word=${BASH_REMATCH[3]}
        done
        [[ -n $word ]] && COMP_WORDS+=("$word")
    done
    COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
    COMP_LINE=$line
    COMP_POINT=${#line}
    # bash finds the compspec of a program typed as a path by its last part.
    fn=$(complete -p "${COMP_WORDS[0]##*/}" | sed -n 's/.* -F \([^ ]*\) .*/\1/p')
    echo "== $name"

    if ((BASH_VERSINFO[0] < 4)); then
        # bash 3.2 sets no COMP_TYPE, so both TABs get the same answer.
        unset COMP_TYPE
        COMPREPLY=()
        APP_DESCRIPTIONS=off "$fn" >/dev/null
        values=("${COMPREPLY[@]}")
        bare=("${COMPREPLY[@]}")
    else
        COMP_TYPE=9
        COMPREPLY=()
        "$fn" >/dev/null
        values=("${COMPREPLY[@]}")

        COMP_TYPE=63
        COMPREPLY=()
        APP_DESCRIPTIONS=off "$fn" >/dev/null
        bare=("${COMPREPLY[@]}")
    fi
    COMPREPLY=()
    "$fn" >"$out"
    listed=("${COMPREPLY[@]}")

    for i in "${!values[@]}"; do
        value=${values[i]} desc=
        if ((${#listed[@]} == ${#values[@]} && ${#bare[@]} == ${#values[@]})); then
            rest=${listed[i]#"${bare[i]}"}
            rest=${rest#"${rest%%[! ]*}"}
            [[ $rest == '('*')' ]] && desc=${rest:1:${#rest}-2}
        fi
        printf '%s\t%s\n' "$value" "$desc"
    done
    # The listing's messages: a blank line, the messages, then "--" or the prompt and line again.
    sed -e '1{/^$/d;}' -e '$d' "$out" | while IFS= read -r msg; do printf '! %s\n' "$msg"; done
done <"$2"
rm -f "$out"
