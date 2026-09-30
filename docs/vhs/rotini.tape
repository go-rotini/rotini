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

# Gruvbox dark, the scheme the site's highlighted code uses: a warm grey that stands apart from
# the page's near-black background, with the site's amber accent for the cursor.
Set Theme { "name": "rotini-gruvbox", "background": "#282828", "foreground": "#ebdbb2", "cursor": "#d09c2b", "selection": "#504945", "black": "#3c3836", "red": "#cc241d", "green": "#98971a", "yellow": "#d79921", "blue": "#458588", "magenta": "#b16286", "cyan": "#689d6a", "white": "#a89984", "brightBlack": "#928374", "brightRed": "#fb4934", "brightGreen": "#b8bb26", "brightYellow": "#fabd2f", "brightBlue": "#83a598", "brightMagenta": "#d3869b", "brightCyan": "#8ec07c", "brightWhite": "#ebdbb2" }

Hide
Type `eval "$(vhs/proxy.sh)" && cd "$(mktemp -d)" && export PS1='\[\e[33m\]$\[\e[0m\] '` Enter
Type `_fill() { perl -pi -e 's/^  summary: TODO.*/  summary: say hello/; s/^  description: TODO.*/  description: helloworld greets you from the command line./' cmd/helloworld/.rotini.spec.yaml; printf '\033[1A\033[2K\r'; }` Enter
Sleep 10s
Type "clear" Enter
Show

Sleep 600ms
Type@55ms "mkdir "
Type@60ms "helloworld "
Sleep 180ms
Type@38ms "&& "
Type@87ms "cd "
Sleep 120ms
Type@41ms "helloworld"
Sleep 350ms
Enter
Sleep 900ms

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
Sleep 3s

Type@75ms "go "
Sleep 180ms
Type@72ms "tool "
Type@71ms "rotini "
Sleep 120ms
Type@60ms "init "
Type@49ms "helloworld"
Sleep 350ms
Enter
Sleep 2s

# Fill in the seed's placeholder text off camera, as a user would before building. The helper
# (defined in the setup) also erases its own command line, so no trace of it is left on screen.
Hide
Type "_fill" Enter
Show

Type@70ms "clear"
Sleep 200ms
Enter
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
Sleep 2s

Type@66ms "go "
Sleep 180ms
Type@69ms "build "
Sleep 90ms
Type@84ms "./cmd/..."
Sleep 350ms
Enter
Sleep 3s

Type@55ms "./helloworld "
Sleep 90ms
Type@72ms "--help"
Sleep 400ms
Enter
Sleep 5s
