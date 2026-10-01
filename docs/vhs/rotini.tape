# The companion-CLI demo that plays in the docs site's footer.
#
# Re-record from the docs directory after a change to the CLI's output:
#
#   cd docs && vhs vhs/rotini.tape
#
# The hidden setup serves this checkout as the published module (see proxy.sh), so the demo runs
# the same commands as the quick start and always shows the rotini in this repository.
#
# VHS types at one fixed speed, so each command is typed in short runs at different speeds
# (Type@<ms>), with small pauses between words, longer ones before a path, and one corrected typo.

Output static/rotini.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 24
Set TypingSpeed 45ms
Set WindowBar Colorful
Set WindowBarSize 36
Set BorderRadius 8
Set Margin 0
Set CursorBlink false
# Each command waits for the prompt to come back before the next is typed, so a slow first run
# (the go tool compiling rotini on its first use) can never have the next command typed over it.

# Gruvbox dark, the scheme the site's highlighted code uses: a warm grey that stands apart from
# the page's near-black background, with the site's amber accent for the cursor.
Set Theme { "name": "rotini-gruvbox", "background": "#282828", "foreground": "#ebdbb2", "cursor": "#d09c2b", "selection": "#504945", "black": "#3c3836", "red": "#cc241d", "green": "#98971a", "yellow": "#d79921", "blue": "#458588", "magenta": "#b16286", "cyan": "#689d6a", "white": "#a89984", "brightBlack": "#928374", "brightRed": "#fb4934", "brightGreen": "#b8bb26", "brightYellow": "#fabd2f", "brightBlue": "#83a598", "brightMagenta": "#d3869b", "brightCyan": "#8ec07c", "brightWhite": "#ebdbb2" }

Hide
Type `eval "$(vhs/proxy.sh)" && cd "$(mktemp -d)" && export PS1='\[\e[33m\]$\[\e[0m\] '` Enter
# Build the rotini tool once off camera, so `go tool rotini init` answers at its real speed
# instead of pausing to compile on first use. The download cache is then emptied (it is the
# throwaway one proxy.sh set up, and the guard refuses any other) so the `go get` on camera
# still downloads; the compiled tool stays in Go's build cache and is reused.
Type `( w=$(mktemp -d) && cd $w && go mod init warm && go get -tool github.com/go-rotini/rotini/cmd/rotini@latest && go tool rotini version ) >/dev/null 2>&1; [ "$GOMODCACHE" = "$ROTINI_DEMO_WORK/modcache" ] && go clean -modcache` Enter
Wait@120s /\$\s*$/
# `go install` lands in a throwaway GOBIN on PATH, never the real one.
Type `export GOBIN="$PWD/bin" && export PATH="$GOBIN:$PATH"` Enter
# Fill in the seed's placeholder text off camera, as a user would before building: a detached
# background helper waits for `rotini init` to write the spec, then edits it. Nothing is typed
# into the visible terminal, and the double subshell keeps bash from printing job messages.
Type `( ( f=helloworld/cmd/helloworld/.rotini.spec.yaml; until grep -qs 'summary: TODO' $f; do sleep 0.2; done; sleep 1; perl -pi -e 's/^  summary: TODO.*/  summary: say hello/; s/^  description: TODO.*/  description: helloworld greets you from the command line./' $f ) & )` Enter
Sleep 10s
Type "clear" Enter
Show

Sleep 600ms
Type@55ms "mkdir "
Type@60ms "helloworld"
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 700ms

Type@87ms "cd "
Sleep 120ms
Type@41ms "helloworld"
Sleep 300ms
Enter
Wait@90s /\$\s*$/
Sleep 800ms

Type@58ms "go "
Sleep 120ms
Type@38ms "mod "
Sleep 120ms
Type@48ms "init "
Sleep 270ms
Type@62ms "github.co"
Type@61ms "m/me/helloworld"
Sleep 400ms
Enter
Wait@90s /\$\s*$/
Sleep 1.2s

Type@39ms "go "
Type@40ms "get "
Sleep 120ms
Type@62ms "-tool "
Sleep 280ms
Type@95ms "github.com/go-roti"
Type@49ms "ni/rotini/cmd/rotini@latest"
Sleep 450ms
Enter
Wait@90s /\$\s*$/
Sleep 1.5s

Type@44ms "go "
Type@58ms "get "
Sleep 260ms
Type@66ms "github.com/go-ro"
Type@47ms "tini/rotini@latest"
Sleep 400ms
Enter
Wait@90s /\$\s*$/
Sleep 1.5s

Type@75ms "go "
Sleep 180ms
Type@72ms "tool "
Type@71ms "rotini "
Sleep 120ms
Type@60ms "init "
Type@49ms "helloworld"
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 2.5s


Type@70ms "clear"
Sleep 200ms
Enter
Wait@90s /\$\s*$/
Sleep 500ms

Type@37ms "ls "
Sleep 120ms
Type@89ms "-A "
Sleep 320ms
Type@61ms "cmd/hel"
Type@44ms "loworld "
Sleep 520ms
Type@71ms "internal"
Type@54ms "/cmd/helloworld"
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 2.5s

Type@70ms "go "
Sleep 180ms
Type@46ms "tool "
Type@72ms "rotini "
Sleep 120ms
Type@75ms "validate "
Sleep 350ms
Type@41ms "cmd/helloworld/."
Type@70ms "rotini.spec.yaml"
Sleep 400ms
Enter
Wait@90s /\$\s*$/
Sleep 2s

Type@52ms "go "
Sleep 150ms
Type@44ms "genr"
Sleep 300ms
Backspace@120ms 1
Sleep 150ms
Type@58ms "erate "
Sleep 90ms
Type@80ms "./..."
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 2s

Type@66ms "go "
Sleep 180ms
Type@69ms "install "
Sleep 90ms
Type@84ms "./cmd/..."
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 1.5s

Type@55ms "helloworld "
Sleep 90ms
Type@72ms "--help"
Sleep 400ms
Enter
Wait@90s /\$\s*$/
Sleep 5s
