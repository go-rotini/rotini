# Completes every case in cases.txt with `commandline complete --detailed`, which asks Nushell for
# a line's completions the way TAB does, through the completer the generated script attaches to
# the app extern. Nushell shows no messages.
# Prints "== <case>", then "<value><TAB><description>" per candidate.
# Usage: nu --no-config-file nu.nu <cases file>
source ../completion_nushell.nu

def main [cases: string] {
    for entry in (open --raw $cases | lines | where {|l| ($l | is-not-empty) and not ($l | str starts-with '#') }) {
        let parts = ($entry | split row "\t")
        print $"== ($parts.0)"
        for c in ($parts.1 | str replace --regex '\|$' '' | commandline complete --detailed) {
            print $"($c.value)\t($c.description? | default '')"
        }
    }
}
