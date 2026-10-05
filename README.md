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

## Rotini in a GIF

Create a Go module, add rotini and run `rotini init`. The program it sets up builds and runs before
you change anything. Then work in the loop. Add a command to the spec and save it, and
`rotini generate --watch` regenerates the code, including the new command's handler stub. Implement
the handler, build and run.

<p align="center">
  <img alt="A split terminal. On the left, a Go module is created, rotini is added and rotini init sets up a program that builds and runs. Then rotini generate --watch starts on the right while a command is added to the spec on the left, its handler is implemented and the program is built and run" src="docs/static/rotini-loop.gif" width="800">
</p>

## Features



## Quick start

Requires Go 1.27 or later.

Create a Go module and add rotini to it, both as a library and as a tool. `go tool rotini init
<name>` then sets up a working program: a spec and a conf, an entrypoint, a handler for each
command, and the generated code. From there, development is a loop: describe a change in the spec
(a command, a flag, an input, an output), run `go generate ./...` to regenerate the typed code,
pages and completion, implement the handler for any new command, and build. Rotini adds nothing
you don't declare: `init` puts `--help` and `--version` in your spec, where you can change them.

<details>
<summary><strong>Example</strong></summary>

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

## Documentation

For more information, see the [rotini](https://rotini.dev) documentation.

- Full API reference is available on [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).
- See [COMPATIBILITY.md](COMPATIBILITY.md) to understand what a version promises and [UPGRADING.md](UPGRADING.md) to understand upgrading versions.
- See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.
- This project follows a code of conduct to ensure a welcoming community. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- To report a vulnerability, see [SECURITY.md](SECURITY.md).
- This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
