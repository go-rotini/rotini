---
title: "rotini"
---

<div class="rotini_text">rotini</div>

<p class="hero_tagline">Define your CLI declaratively,<br>write its behavior imperatively.</p>

<p class="hero_beats">Validate. Generate. Ship.</p>

<p class="hero_sub">Rotini is a spec-driven codegen package for building CLI programs in Go. You describe your commands, flags and arguments in a spec file (YAML, JSON, JSONC or TOML). Rotini checks the spec and generates the typed Go code, the command tree and a handler file for each command. You write what each command does, and the runtime parses and validates the user's input before your code reads it. The loop is: edit the spec, generate, implement the handlers, build.</p>

---

## Features

| Feature | What you get |
|:---|:---|
| **Commands** | Sub-commands to any depth, with aliases, help groups and hidden commands. |
| **Flags and arguments** | Short and long flags with GNU-style parsing (`-abc`, `--name=value`, `--no-x`), repeatable flags, and optional and variadic arguments. |
| **Typed values** | Strings, numbers, booleans, lists and maps, plus value types such as `duration`, `date`, `url`, `ip`, `bytesize` and `existingfile`. |
| **Validation** | Required values, defaults, enums, patterns, bounds and lengths, plus flags that are mutually exclusive, required together, one-of or at-least-one, and flags that require others. A bad value is a usage error naming the flag the user typed. |
| **Environment variables** | Any flag can fall back to an environment variable, named from a prefix or set exactly. |
| **Config files** | Values from YAML, JSON, JSONC, TOML or dotenv files at a fixed path or discovered in the XDG config directory or by walking up from the working directory. The command line always wins. |
| **Stdin** | A typed payload piped on stdin, as a JSON, YAML, JSONC or TOML document, as text, or as lines. |
| **Secrets** | Inputs marked secret are redacted from errors and from the record of where each value came from. |
| **Help** | `--help` and `help <command>` pages with usage, examples and see-also links, under headings you can rename, or a page you write yourself. |
| **Version** | `--version` and a `version` command, stamped at build time. |
| **Shell completion** | Scripts for bash, zsh, fish and PowerShell, with completion hints for values such as files and directories. |
| **Man and markdown pages** | Roff man pages and markdown reference pages, ready to install or publish. |
| **Structured output** | A JSON Schema for each command's output and a contract document describing the whole CLI, for scripts and agents. |
| **Errors and exit codes** | Consistent `Error:` messages and a non-zero exit code. Every error carries a usage or internal category you can map to your own exit codes, and errors can be reported as JSON for scripts. |
| **Deprecation** | Deprecated commands, aliases and flags keep working and are marked in help. Each use is reported to your code, which decides whether to warn. |
| **Interrupts and panics** | Ctrl+C and SIGTERM stop the program cleanly, running its teardown, and a second Ctrl+C exits at once. A panic is reported as an error rather than a stack trace. |
| **Suggestions** | "Did you mean" suggestions for a mistyped command or flag, opt-in. |
| **Plugins** | Run separate `<app>-<name>` programs as sub-commands, declared or discovered. A rotini program can also be a plugin for kubectl, Docker or Flux, completing and showing help the way the host does. |
| **Wrapper commands** | A command that forwards everything after its name untouched to another program. |
| **Composed CLIs** | Mount one CLI inside another as a sub-command, from the same module or another, while it still builds and ships on its own. |

---

## Quick start

Requires Go 1.27 or later. This builds `helloworld`, a program with one command, `hello`.

### 1. Set up the program

Create a Go module and add the rotini tool to it. `rotini init` sets up a program that builds and
runs as it is, and `go mod tidy` records rotini as a direct dependency, since its code imports it:

{{< code title="terminal" language="bash" open="true" collapsible="false" copy="true" >}}
mkdir helloworld
cd helloworld
go mod init github.com/me/helloworld
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go tool rotini init helloworld
go mod tidy
{{< /code >}}

{{< code title="what init writes" language="text" open="true" collapsible="false" copy="false" >}}
cmd/helloworld/
  .rotini.spec.yaml        the spec: what the CLI accepts
  .rotini.conf.yaml        the conf: what is generated, and where
  .rotini-schema.*.json    JSON Schemas, for editor completion
  main.go                  the entrypoint
internal/cmd/helloworld/
  zz_rotini.go             generated on every run; do not edit
  helloworld*.go           one handler file per command; yours to edit
{{< /code >}}

### 2. Declare a command

Add a `hello` command, with an optional argument and a flag, under `commands:` in the spec:

{{< code title="cmd/helloworld/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
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
{{< /code >}}

### 3. Generate, then implement the handler

`go generate ./...` writes `internal/cmd/helloworld/helloworld_hello.go`. Its `Run` method already
reads the typed, validated inputs; `--help` is answered for every command by the root handler
`init` wrote. Replace the line that prints the inputs with the greeting code, and add `"strings"`
to the imports:

{{< code title="internal/cmd/helloworld/helloworld_hello.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*helloworldHelloHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[HelloworldHelloInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	greeting := "Hello, " + inputs.HelloworldHello.Arguments.Name + "!"
	if inputs.HelloworldHello.Flags.Shout {
		greeting = strings.ToUpper(greeting)
	}
	fmt.Fprintln(rtx.Stdout, greeting)
}
{{< /code >}}

### 4. Build and run

{{< code title="terminal" language="console" open="true" collapsible="false" copy="false" >}}
$ go build ./cmd/helloworld
$ ./helloworld hello
Hello, world!
$ ./helloworld hello rotini --shout
HELLO, ROTINI!
{{< /code >}}

From here, every change follows the same loop: change the spec, run `go generate ./...`, implement
any new handler, and build.

---

## Next

- [README.md](/docs): the guide to using rotini, from install to testing
- [.rotini.spec.yaml](/specification): every spec key
- [.rotini.conf.yaml](/configuration): every conf key
- [zz_rotini.go](/generated): what `rotini generate` writes, and which files are yours
- [api.go](/api): what you call from `main.go` and your handlers
- [cli](/cli): the `rotini` companion CLI
