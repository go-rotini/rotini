# rotini

rotini is a CLI framework for Go in which you **declare** your command-line interface and
**write** only what its commands do.

The commands, flags, arguments, environment variables and configuration files your CLI
accepts live in one spec file. `rotini validate` checks that spec — a JSON Schema plus 43 lint
rules, each problem reported with a `file:line:col` — before any code exists. `rotini generate`
turns it into typed Go structs, the command tree, help pages (and, if you want them, man pages,
markdown and shell completion), and one handler file per command. You fill in the handlers;
the runtime parses, validates and reconciles every input into the typed struct before your
code runs.

One module provides both halves at one version, so they cannot drift: the **`rotini` tool**
(`init`, `generate`, `validate`) and the **runtime library** your generated code imports.

## Quick Start

Requires **Go 1.27** or later.

### 1. Initialize

```bash
mkdir helloworld
cd helloworld
go mod init github.com/me/helloworld

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go tool rotini init helloworld
```

`go get -tool` adds rotini to your `go.mod` as a tool — which also brings in the runtime the
generated code imports. `rotini init` then writes a working CLI:

```
cmd/helloworld/
  .rotini.spec.yaml          the spec: what the CLI accepts
  .rotini.conf.yaml          the conf: where generated code goes; which extras to produce
  .rotini-schema.*.json      JSON Schemas your editor uses to check and complete both files
  main.go                    the entrypoint
internal/cmd/helloworld/
  zz_rotini.go               generated — rewritten on every `go generate`, never edit it
  helloworld.go              one handler file per command — created once, then yours
  helloworld_help.go
  helloworld_version.go
```

### 2. Review the generated spec file and modify

The seeded spec already declares `--help`, `--version`, and `help` and `version` commands:

```yaml
$schema: ./.rotini-schema.spec.json
version: 0.0.0
command:
  name: helloworld
  summary: TODO — one line, shown next to this command in a parent's command list
  description: TODO — the long description at the top of `helloworld --help`
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

Replace the two `TODO` lines with your own text, then grow the CLI by declaring what it
accepts. For example, add a `hello` command under `commands:`:

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

Because the spec names its schema in `$schema`, your editor completes and checks every key as
you type. `go tool rotini validate` checks the whole spec from the command line.

### 3. Review the generated conf file and modify

The conf says where generated code is written and which extras to produce:

```yaml
$schema: ./.rotini-schema.conf.json
version: 0.0.0
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
    # help is on so `helloworld --help` works from the first build. The other three
    # are opt-in; flip `enabled` to turn one on.
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

Set `enabled: true` on `completion`, `man` or `markdown` to generate shell completion scripts,
man pages or markdown docs from the same spec.

### 4. Review the generated handler file and modify

Each command has its own handler file. After you add a command to the spec, `go generate ./...`
creates a stub for it — here, `internal/cmd/helloworld/helloworld_hello.go`. A stub is
created once and never overwritten, so it is yours to edit. It already answers `--help` and
collects the command's inputs; replace the `TODO` at the end with what the command does:

```go
func (*helloworldHelloHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rotini.ParseArgv[HelloworldHelloInputs](rtx); err == nil {
		if argv.Values.HelloworldHello.Flags.Help {
			fmt.Fprintln(rtx.Stdout, rtx.Help())
			rtx.HaltWithCode(0)
			return
		}
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

`HelloworldHelloInputs` is generated from the spec, so `Name` is a `string` and `Shout` a
`bool` — the compiler holds the handler to the spec. `rotini.Collect` fills it from the command
line, environment variables, configuration files and defaults, validated, in one documented
order.

### 5. Review the generated main file and modify

`main.go` is the entrypoint. It is created once, and its `//go:generate` line is what makes
`go generate ./...` turn spec into generated code.

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

`version` is what `--version` and `helloworld version` report; stamp it at build time with the
`-ldflags` shown in the comment.

### 6. Generate, build, and run

```bash
go generate ./...
go build ./cmd/... # or: go install ./cmd/...

./helloworld hello
./helloworld hello rotini --shout
./helloworld hello --help
```

```
hello, world
HELLO, ROTINI
say hello

Usage:
  helloworld hello [name] [flags]

Arguments:
  [name]    who to greet (default world)

Flags:
  -s, --shout    greet in capitals
  -h, --help     print help
```

From here, the loop is always the same: change the spec, run `go generate ./...`, fill in any
new handler, build.

## Documentation

For guides, the full spec and conf references and worked examples, see the
[rotini documentation](https://rotini.dev).

- The full API reference is available on
  [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).
- See [COMPATIBILITY.md](COMPATIBILITY.md) to understand what a version promises and
  [UPGRADING.md](UPGRADING.md) to understand upgrading between versions.
- See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.
- This project follows a code of conduct to ensure a welcoming community. See
  [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- To report a vulnerability, see [SECURITY.md](SECURITY.md).
- This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
