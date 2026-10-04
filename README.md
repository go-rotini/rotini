<h1 align="center">rotini</h1>

<p align="center"><strong>A spec-driven codegen package for building CLI programs in Go.</strong></p>

<p align="center">
  <a href="https://github.com/go-rotini/rotini/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/go-rotini/rotini?sort=semver&color=007d9c&labelColor=5c5c5c&style=flat-square"></a>
  <a href="https://rotini.dev"><img alt="Documentation" src="https://img.shields.io/badge/docs-rotini.dev-007d9c?labelColor=5c5c5c&style=flat-square"></a>
  <a href="https://pkg.go.dev/github.com/go-rotini/rotini"><img alt="Go Reference" src="https://img.shields.io/badge/-reference-007d9c?logo=go&logoColor=white&labelColor=5c5c5c&style=flat-square"></a>
  <a href="LICENSE"><img alt="MIT license" src="https://img.shields.io/badge/license-MIT-007d9c?labelColor=5c5c5c&style=flat-square"></a>
</p>

Rotini lets you describe a command-line program in a JSON schema specification format and generates a Go program around
it. You provide the implementation for what each command does, and rotini handles the CLI program plumbing. Rather than investing time into writing the plumbing that supports a Go CLI program, you can focus on writing your program specific logic. However, rotini does not enforce its structure and was written with inversion of control and dependency injection in mind; you can adopt as much or as little of rotini as you want. If you buy-in to the lightest commitement of the frame - the spec-driven codegen model - you can bring your own command routing, parsing, input handling, help and error reporting, while rotini still generates the command tree, typed inputs, documentation and shell completion from your spec.

<p align="center">
  <img alt="Creating, generating and running a rotini program in a terminal" src="docs/static/rotini.gif" width="800">
</p>

## One spec. A complete command.

This spec declares a `hello` command with an optional argument and a flag:

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
```

`rotini generate` turns it into a typed inputs struct, the parsing and validation code, the help
page and the shell completion. It also writes a handler stub, which becomes yours. Your complete
`helloworld_hello.go` is ordinary Go:

```go
package helloworld

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*helloworldHelloHandler)(nil)

type helloworldHelloHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*helloworldHelloHandler) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rtx.ArgvInputs[HelloworldHelloInputs](); err == nil && argv.Values.Helloworld.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	inputs, err := rtx.Inputs[HelloworldHelloInputs]()
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

The help, the defaults and the error for a mistyped flag all come from the spec:

```console
$ helloworld hello Gopher --shout
HELLO, GOPHER

$ helloworld hello --help
say hello

Usage:
  helloworld hello [name] [flags]

Arguments:
  [name]    who to greet (default world)

Flags:
  -s, --shout    greet in capitals

$ helloworld hello --loud
Error: unknown flag "--loud"
```

Change the spec and regenerate: the inputs struct, help and completion change with it, and the
handler you wrote is never overwritten.

## Quick start

Requires Go 1.27 or later.

Create a Go module and add rotini to it, both as a library and as a tool. `go tool rotini init
<name>` then sets up a working program: a spec and a conf, an entrypoint, a handler for each
command, and the generated code. From there, development is a loop: describe a change in the spec
(a command, a flag, an input, an output), run `go generate ./...` to regenerate the typed code,
pages and completion, implement the handler for any new command, and build. Rotini adds nothing
you don't declare: `init` puts `--help` and `--version` in your spec, where you can change them.

<details>
<summary><strong>Build the <code>helloworld</code> example above, step by step</strong></summary>

**1. Create a module and add rotini.**

```bash
mkdir helloworld && cd helloworld
go mod init github.com/me/helloworld
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go get github.com/go-rotini/rotini@latest
```

**2. Set up the program.**

```bash
go tool rotini init helloworld
```

```
cmd/helloworld/.rotini.spec.yaml     the spec: what the CLI accepts
cmd/helloworld/.rotini.conf.yaml     the conf: what is generated, and where
cmd/helloworld/main.go               the entrypoint
internal/cmd/helloworld/zz_rotini.go generated on every run; do not edit
internal/cmd/helloworld/*.go         one handler per command; yours to edit
```

**3. Declare the command.** Add the `hello` command shown above under `commands:` in
`cmd/helloworld/.rotini.spec.yaml`.

**4. Generate, then implement the handler.** `go generate ./...` writes
`internal/cmd/helloworld/helloworld_hello.go`. Its `Run` method already answers `--help` and reads
the typed, validated inputs. Replace the line that prints them with the greeting code, as in the
complete file above.

**5. Build and run.**

```console
$ go build ./cmd/helloworld
$ ./helloworld hello --shout rotini
HELLO, ROTINI
```

</details>

For the full walkthrough, see [Getting started](https://rotini.dev/docs/).

## What you declare, what you get

| Declare in the spec | Get from rotini |
|---|---|
| Commands, aliases, groups, hidden and deprecated commands | The command tree and dispatch, help listings and completion, deprecation reported as data |
| Flags and arguments, with types, defaults, enums and bounds | Typed Go fields, GNU-style parsing (`-abc`, `--name=value`, `--no-x`), and values validated before your code uses them |
| Environment variables, config files and stdin | Values read in one documented order (defaults < config files < environment < command line), with a record of where each came from |
| Help text, examples and see-also links | Help pages, roff man pages and markdown pages, all from the same source |
| Output shapes and exit statuses | Output types, an OUTPUT section on every page, a JSON Schema per output, and a contract document for scripts and agents |
| Plugins | Dispatch to `<app>-<name>` binaries, declared or discovered, and completion through kubectl, Docker and Flux |
| Other specs, in this module or another | One CLI composed from several specs with `$ref` and `mod://` |

Built into the runtime: five lifecycle hooks with reverse teardown, signal handling and panic
recovery on by default, typed errors with usage and internal categories that set the exit code,
typed dependencies shared between handlers, and in-process testing with no global state.

## Keep exploring

- **Learn the workflow:** [Getting started](https://rotini.dev/docs/) and the
  [generated code](https://rotini.dev/generated/).
- **Look up a key:** the [spec reference](https://rotini.dev/specification/) and the
  [conf reference](https://rotini.dev/configuration/).
- **Use the tools:** the [`rotini` command](https://rotini.dev/cli/) and the
  [Go API](https://rotini.dev/api/) ([pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini)).
- **Go further:** [structured output](https://rotini.dev/output/) and
  [plugins for kubectl, Docker and Flux](https://rotini.dev/plugins/).
- **Know what a version promises:** [COMPATIBILITY.md](COMPATIBILITY.md).

## Contributing

Questions, bug reports and pull requests are welcome.

- **Report a bug or ask a question:** [open an issue](https://github.com/go-rotini/rotini/issues).
- **Contribute code:** [CONTRIBUTING.md](CONTRIBUTING.md) covers setup and the checks a pull
  request must pass (`make all`).
- **Report a vulnerability:** see [SECURITY.md](SECURITY.md).

This project follows a [code of conduct](CODE_OF_CONDUCT.md). MIT licensed; see
[LICENSE](LICENSE).
