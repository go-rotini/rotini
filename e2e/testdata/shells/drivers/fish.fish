# Completes every case in cases.txt with `complete -C`, which asks fish for a line's completions
# the way TAB does, through the generated script and fish's own helpers; --escape gives each
# value as fish inserts it. fish shows no messages.
# Prints "== <case>", then "<value><TAB><description>" per candidate.
# Usage: fish --no-config fish.fish <completion script> <cases file>
source $argv[1]
while read -l entry
    if test -z "$entry"; or string match -q '#*' -- $entry
        continue
    end
    set -l parts (string split \t -- $entry)
    echo "== $parts[1]"
    for c in (complete --escape -C (string replace -r '\|$' '' -- $parts[2]))
        set -l f (string split -m 1 \t -- $c)
        printf '%s\t%s\n' $f[1] "$f[2]"
    end
end <$argv[2]
