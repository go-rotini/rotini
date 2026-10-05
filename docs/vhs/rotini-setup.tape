# The README's setup demo: create a module, add rotini, run `rotini init`, list what it wrote, then
# build the untouched program and show its help.
#
# Re-record from the docs directory after a change to the CLI's output:
#
#   cd docs && vhs vhs/rotini-setup.tape
#
# Like rotini.tape, the hidden setup serves this checkout as the published module (see proxy.sh),
# and each command is typed in short runs at different speeds.

Output static/rotini-setup.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 720
Set Padding 24
Set TypingSpeed 45ms
Set WindowBar Colorful
Set WindowBarSize 36
Set BorderRadius 8
Set Margin 0
Set CursorBlink false
Set Framerate 25

Set Theme { "name": "rotini-gruvbox", "background": "#282828", "foreground": "#ebdbb2", "cursor": "#d09c2b", "selection": "#504945", "black": "#3c3836", "red": "#cc241d", "green": "#98971a", "yellow": "#d79921", "blue": "#458588", "magenta": "#b16286", "cyan": "#689d6a", "white": "#a89984", "brightBlack": "#928374", "brightRed": "#fb4934", "brightGreen": "#b8bb26", "brightYellow": "#fabd2f", "brightBlue": "#83a598", "brightMagenta": "#d3869b", "brightCyan": "#8ec07c", "brightWhite": "#ebdbb2" }

Hide
Type `eval "$(vhs/proxy.sh)" && cd "$(mktemp -d)" && export PS1='\[\e[33m\]$\[\e[0m\] '` Enter
# Build the rotini tool once off camera so `go tool rotini init` answers at its real speed, then
# empty the throwaway download cache so the `go get` on camera still downloads.
Type `( w=$(mktemp -d) && cd $w && go mod init warm && go get -tool github.com/go-rotini/rotini/cmd/rotini@latest && go tool rotini version ) >/dev/null 2>&1; [ "$GOMODCACHE" = "$ROTINI_DEMO_WORK/modcache" ] && go clean -modcache` Enter
Wait@120s /\$\s*$/
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

Type@70ms "clear"
Sleep 200ms
Enter
Wait@90s /\$\s*$/
Sleep 500ms

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

Type@66ms "go "
Sleep 160ms
Type@58ms "build "
Sleep 120ms
Type@84ms "./cmd/..."
Sleep 350ms
Enter
Wait@90s /\$\s*$/
Sleep 1.2s

Type@60ms "./hello"
Type@48ms "world "
Sleep 150ms
Type@72ms "--help"
Sleep 400ms
Enter
Wait@90s /\$\s*$/
Sleep 6s
