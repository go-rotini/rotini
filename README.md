# rotini

Rotini is a spec-driven CLI package for Go, with a codegen tool and a runtime library. You declare your commands, flags and arguments in a spec file (YAML, JSON, JSONC or TOML). Rotini validates it and generates the typed Go, the command tree and a handler stub per command. You write each command's handler, and the runtime parses and validates input before calling it. The loop is: edit the spec, `go generate`, provide/update handler implementations, build.

## Quick Start

Requires **Go 1.27** or later.

### 1. Initialize

```bash
mkdir helloworld
cd helloworld
go mod init github.com/me/helloworld

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go get github.com/go-rotini/rotini@latest
go tool rotini init helloworld
```

This creates:

```
cmd/helloworld/
  .rotini.spec.yaml        the spec (what the CLI accepts)
  .rotini.conf.yaml        the conf  (where generated code goes)
  .rotini-schema.*.json    schemas for editor completion
  main.go                  the entrypoint
internal/cmd/helloworld/
  zz_rotini.go             generated on every `go generate`; do not edit
  helloworld.go            one handler file per command; yours to edit
  helloworld_help.go
  helloworld_version.go
```

### 2. Review the generated spec file and modify

```yaml
$schema: ./.rotini-schema.spec.json
version: 1.0.0
command:
  name: helloworld
  summary: TODO — what helloworld does, in one line
  description: TODO — the paragraph at the top of `helloworld --help`
  footer: Use "helloworld help <command>" for more information about a command.
  flags:
    - name: help
      summary: print help
      identifiers: [-h, --help]
      schema: { type: bool }
    - name: version
      summary: print version
      identifiers: [-v, --version]
      schema: { type: bool }
  commands:
    - name: help
      summary: print help
      description: Print help for a command.
      arguments:
        - name: command
          summary: the command path to print help for
          schema: { type: '[]string' }
      flags:
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
    - name: version
      summary: print version
      description: Print the helloworld version.
      flags:
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
```

Fill in the `TODO`s, then add commands under `commands:` — for example:

```yaml
    - name: hello
      summary: say hello
      arguments:
        - name: name
          summary: who to greet
          schema: { type: string, default: world }
      flags:
        - name: shout
          summary: greet in capitals
          identifiers: [-s, --shout]
          schema: { type: bool }
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
```

### 3. Review the generated conf file and modify

```yaml
$schema: ./.rotini-schema.conf.json
version: 1.0.0
generate:
  schemas:
    conf:
      file: cmd/helloworld/.rotini-schema.conf.json
    spec:
      file: cmd/helloworld/.rotini-schema.spec.json
  packages:
    - type: main
      file: cmd/helloworld/main.go
    - type: cmd
      file: internal/cmd/helloworld/zz_rotini.go
  features:
    - type: help
      enabled: true
    - type: completion
      enabled: false
    - type: man
      enabled: false
    - type: markdown
      enabled: false
validate:
  fail: collect
```

Enable `completion`, `man` or `markdown` to generate those from the spec too.

### 4. Review the generated handler file and modify

`go generate ./...` creates a handler file for each new command — here
`internal/cmd/helloworld/helloworld_hello.go`. Replace the line that prints `inputs` with the
command's work, and add `"strings"` to its imports:

```go
func (*helloworldHelloHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rotini.ParseArgv[HelloworldHelloInputs](rtx); err == nil && argv.Values.HelloworldHello.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	inputs, err := rotini.Collect[HelloworldHelloInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	greeting := "hello, " + inputs.HelloworldHello.Arguments.Name
	if inputs.HelloworldHello.Flags.Shout {
		greeting = strings.ToUpper(greeting)
	}
	fmt.Fprintln(rtx.Stdout, greeting)
}
```

### 5. Review the generated main file and modify

```go
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "github.com/me/helloworld/internal/cmd/helloworld"
)

// go build -ldflags "-X main.version=1.2.3" ./cmd/...
var version = "0.0.0"

func main() {
	cmd.Program.
		WithVersion(version).
		Execute()
}
```

### 6. Generate, build, and run

```bash
go generate ./...
go build ./cmd/... # or: go install ./cmd/...

./helloworld hello --shout rotini   # HELLO, ROTINI
./helloworld --help
```

Change the spec, `go generate ./...`, fill in any new handler, build — that's the whole loop.

## Documentation

For more information, see the [rotini](https://rotini.dev) documentation.

- Full API reference is available on [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).
- See [COMPATIBILITY.md](COMPATIBILITY.md) to understand what a version promises and [UPGRADING.md](UPGRADING.md) to understand upgrading versions.
- See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.
- This project follows a code of conduct to ensure a welcoming community. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- To report a vulnerability, see [SECURITY.md](SECURITY.md).
- This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
