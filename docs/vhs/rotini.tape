# The companion-CLI demo that plays in the docs site's footer.
#
# Re-record from the docs directory after a change to the CLI's output:
#
#   cd docs && vhs vhs/rotini.tape
#
# The hidden setup builds the demo module against this checkout, so the recording always shows
# the rotini in this repository.

Output static/rotini.gif

Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 24
Set TypingSpeed 45ms
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful

Hide
Type "export ROTINI_SRC=$(cd .. && pwd) && cd $(mktemp -d) && export PS1='$ '" Enter
Type "go mod init example.com/hello >/dev/null 2>&1" Enter
Type "go mod edit -require github.com/go-rotini/rotini@v0.0.0 -replace github.com/go-rotini/rotini=$ROTINI_SRC -tool github.com/go-rotini/rotini/cmd/rotini" Enter
Type "go mod tidy >/dev/null 2>&1 && go tool rotini version >/dev/null" Enter
Type `_fill() { perl -pi -e 's/^  summary: TODO.*/  summary: say hello/; s/^  description: TODO.*/  description: hello greets you from the command line./' cmd/hello/.rotini.spec.yaml; printf '\033[1A\033[2K\r'; }` Enter
Sleep 20s
Type "clear" Enter
Show

Type "go tool rotini init hello"
Sleep 400ms
Enter
Sleep 2s

# Fill in the seed's placeholder text off camera, as a user would before building. The helper
# (defined in the setup) also erases its own command line, so no trace of it is left on screen.
Hide
Type "_fill" Enter
Show

Type "ls -A cmd/hello internal/cmd/hello"
Sleep 400ms
Enter
Sleep 2.5s

Type "go tool rotini validate cmd/hello/.rotini.spec.yaml --config cmd/hello/.rotini.conf.yaml"
Sleep 400ms
Enter
Sleep 2.5s

Type "go generate ./... && go build ./cmd/hello"
Sleep 400ms
Enter
Sleep 4s

Type "./hello --help"
Sleep 400ms
Enter
Sleep 5s
