# The README's demo, from an empty directory to the development loop, in one split terminal:
#
#   1. Set up, in the left pane: create a module, add rotini, run `rotini init`, then build and run
#      the untouched program.
#   2. Start `rotini generate --watch` in the right pane.
#   3. Loop: add a command to the spec on the left and save; the right pane regenerates, writing
#      the new command's handler stub. Implement it, build, and run.
#
# Re-record from the docs directory after a change to the CLI's output:
#
#   cd docs && vhs vhs/rotini-loop.tape
#
# The hidden setup serves this checkout as the published module (see proxy.sh) and starts tmux on
# its own socket with the configs in loop/, so no personal shell, tmux or nvim configuration is
# read. tmux keys use its default prefix, Ctrl+B; o moves to the other pane.

Output static/rotini-loop.gif

Set Shell "bash"
Set FontSize 15
Set Width 1400
Set Height 760
Set Padding 24
Set TypingSpeed 45ms
Set WindowBar Colorful
Set WindowBarSize 36
Set BorderRadius 8
Set Margin 0
Set CursorBlink false
Set Framerate 20

Set Theme { "name": "rotini-gruvbox", "background": "#282828", "foreground": "#ebdbb2", "cursor": "#d09c2b", "selection": "#504945", "black": "#3c3836", "red": "#cc241d", "green": "#98971a", "yellow": "#d79921", "blue": "#458588", "magenta": "#b16286", "cyan": "#689d6a", "white": "#a89984", "brightBlack": "#928374", "brightRed": "#fb4934", "brightGreen": "#b8bb26", "brightYellow": "#fabd2f", "brightBlue": "#83a598", "brightMagenta": "#d3869b", "brightCyan": "#8ec07c", "brightWhite": "#ebdbb2" }

Hide
Type `export DEMO="$PWD/vhs/loop" && eval "$(vhs/proxy.sh)" && cd "$(mktemp -d)" && export PS1='\[\e[33m\]$\[\e[0m\] '` Enter
Type `export XDG_CONFIG_HOME="$DEMO/xdg" XDG_DATA_HOME="$ROTINI_DEMO_WORK/xdg/data" XDG_STATE_HOME="$ROTINI_DEMO_WORK/xdg/state"` Enter
# `go install` on camera writes to a throwaway bin directory, put first on PATH.
Type `export GOBIN="$ROTINI_DEMO_WORK/bin" && export PATH="$GOBIN:$PATH"` Enter
# Build the rotini tool once so `go tool rotini init` answers at its real speed, then empty the
# throwaway download cache so the `go get` on camera still downloads.
Type `( w=$(mktemp -d) && cd $w && go mod init warm && go get -tool github.com/go-rotini/rotini/cmd/rotini@latest && go tool rotini version ) >/dev/null 2>&1; [ "$GOMODCACHE" = "$ROTINI_DEMO_WORK/modcache" ] && go clean -modcache; echo ready` Enter
Wait+Screen@300s /ready/
Type `bash "$DEMO/start-tmux.sh"` Enter
Sleep 2s
Show

# 1. Set up, in the left pane.
Sleep 600ms
Type@55ms "mkdir "
Type@60ms "helloworld"
Sleep 350ms
Enter
Sleep 700ms
Type@87ms "cd "
Sleep 120ms
Type@41ms "helloworld"
Sleep 300ms
Enter
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
Sleep 1s
Type@70ms "clear"
Sleep 200ms
Enter
Sleep 600ms

Type@75ms "go "
Sleep 180ms
Type@72ms "tool "
Type@71ms "rotini "
Sleep 120ms
Type@60ms "init "
Type@49ms "helloworld"
Sleep 350ms
Enter
Sleep 800ms
Type@52ms "go "
Sleep 140ms
Type@64ms "mod "
Type@58ms "tidy"
Sleep 350ms
Enter
Sleep 800ms
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
Sleep 800ms
Type@66ms "go "
Sleep 160ms
Type@58ms "build "
Sleep 120ms
Type@84ms "./cmd/..."
Sleep 350ms
Enter
Sleep 800ms
Type@60ms "./hello"
Type@48ms "world "
Sleep 150ms
Type@72ms "--help"
Sleep 400ms
Enter
Sleep 800ms

# 2. Watch mode on the right.
Ctrl+B
Type "o"
Sleep 600ms
Type@60ms "cd helloworld/cmd/helloworld"
Sleep 300ms
Enter
Sleep 500ms
Type@55ms "go tool rotini "
Sleep 150ms
Type@60ms "generate "
Sleep 200ms
Type@45ms "--watch"
Sleep 400ms
Enter
Sleep 2.5s

# 3. Loop: add a command to the spec.
Ctrl+B
Type "o"
Sleep 500ms
Type@70ms "clear"
Enter
Sleep 500ms
Type@55ms "nvim cmd/helloworld/.rotini.spec.yaml"
Sleep 300ms
Enter
Sleep 1.5s
Type@70ms "/  commands:"
Enter
Sleep 600ms
Type "o"
Sleep 200ms
Type@70ms "    - name: hello"
Enter
Sleep 300ms
Type@70ms "      summary: say hello"
Enter
Sleep 300ms
Type@70ms "      arguments:"
Enter
Sleep 300ms
Type@70ms "        - name: name"
Enter
Sleep 300ms
Type@70ms "          summary: who to greet"
Enter
Sleep 300ms
Type@70ms "          schema: { type: string, default: world }"
Enter
Sleep 300ms
Type@70ms "      flags:"
Enter
Sleep 300ms
Type@70ms "        - name: shout"
Enter
Sleep 300ms
Type@70ms "          summary: greet in capitals"
Enter
Sleep 300ms
Type@70ms "          identifiers: [-s, --shout]"
Enter
Sleep 300ms
Type@70ms "          schema: { type: bool }"
Sleep 400ms
Escape
Sleep 300ms
Type@80ms ":wq"
Enter
Sleep 3s

# Implement the new command's handler, which the regeneration just wrote.
Type@55ms "nvim internal/cmd/helloworld/helloworld_hello.go"
Sleep 300ms
Enter
Sleep 1.5s
Type@70ms `/"fmt"`
Enter
Sleep 400ms
Type "o"
Tab
Type@70ms `"strings"`
Escape
Sleep 600ms
Type@70ms "/Fprintf"
Enter
Sleep 800ms
Type "cc"
Tab
Type@70ms `greeting := "Hello, " + inputs.HelloworldHello.Arguments.Name + "!"`
Enter
Sleep 300ms
Tab
Type@70ms "if inputs.HelloworldHello.Flags.Shout {"
Enter
Sleep 300ms
Tab 2
Type@70ms "greeting = strings.ToUpper(greeting)"
Enter
Sleep 300ms
Tab
Type "}"
Enter
Sleep 300ms
Tab
Type@70ms "fmt.Fprintln(rtx.Stdout, greeting)"
Sleep 400ms
Escape
Sleep 300ms
Type@80ms ":wq"
Enter
Sleep 800ms

# Build and run, then install and run it from PATH.
Type@60ms "go build ./cmd/..."
Sleep 300ms
Enter
Sleep 800ms
Type@55ms "./helloworld hello --help"
Sleep 300ms
Enter
Sleep 800ms
Type@60ms "go install ./cmd/..."
Sleep 300ms
Enter
Sleep 800ms
Type@55ms "helloworld hello"
Sleep 300ms
Enter
Sleep 800ms
Type@55ms "helloworld hello rotini --shout"
Sleep 300ms
Enter
Sleep 4s
Type@70ms "clear"
Sleep 200ms
Enter
Sleep 600ms

Hide
Type "tmux -L rotini-demo kill-server" Enter
Sleep 500ms
