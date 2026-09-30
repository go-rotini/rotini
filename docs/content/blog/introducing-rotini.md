---
title: "Declare the CLI, write the commands"
date: 2026-09-29
draft: true
author_name: "Matt Getz"
author_image: "https://github.com/matthewgetz.png"
author_tagline: "rotini enjoyer"
---

A command-line tool is two things. There is its **surface** — the commands, the flags and
arguments, the environment variables and configuration files it reads, its help pages and
shell completion. And there is its **behaviour** — what each command actually does.

rotini is built on one idea: you should *declare* the first and *write* the second.

## Declaring the surface

The surface of a CLI is data. A flag has a name, a type, maybe a default, maybe an
environment variable it falls back to. Written as code, that data is scattered across
registration calls and struct tags, and nothing checks it until someone runs the binary.
Written as a spec, it is one file you can read top to bottom:

```yaml
version: 0.0.0
command:
  name: todo
  summary: a task list
  commands:
    - name: add
      summary: add a task
      arguments:
        - name: title
          schema: {type: string, required: true}
      flags:
        - name: due
          identifiers: [--due, -d]
          schema: {type: date}
```

Because it is data, it can be **checked before any code exists**. `rotini validate` runs the
spec through a JSON Schema and 43 lint rules, and every problem comes back with a position:

```
Error: spec: cmd/todo/.rotini.spec.yaml:37:7: command todo/add: flag "due": type "dat" is not a
type rotini knows — "dat" is neither a Go builtin nor a rotini type; did you mean "date"?
```

And because it is data, everything that describes the surface comes from the same place:
the typed Go structs your handlers read, the `--help` pages, man pages, markdown docs and
shell completion scripts. Rename a flag in the spec, run `go generate`, and every one of them
changes together.

## Four files, each with one job

A rotini CLI is four kinds of file, and each is small enough to hold in your head.

**The spec** — `.rotini.spec.yaml` — is what the CLI accepts: the file above.

**The conf** — `.rotini.conf.yaml` — is where the generated code goes and which extras to
produce:

```yaml
version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/todo/main.go
    - type: cmd
      file: internal/cmd/todo/zz_rotini.go
  features:
    - type: help
      enabled: true
```

**`main.go`** is the entrypoint, and it is exactly as short as it looks:

```go
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import cmd "example.com/todo/internal/cmd/todo"

var version = "0.0.0"

func main() {
	cmd.Program.
		WithVersion(version).
		Execute()
}
```

**A handler file per command** is where the behaviour lives. rotini writes it once, already
able to answer `--help`, and then it is yours — regenerating never overwrites it. What you
add is the command itself:

```go
in, err := rotini.Collect[TodoAddInputs](rtx)
if err != nil {
	rtx.HaltWith(err)
	return
}
fmt.Fprintf(rtx.Stdout, "added %q, due %s\n", in.TodoAdd.Arguments.Title, in.TodoAdd.Flags.Due.Format(time.DateOnly))
```

`TodoAddInputs` is generated from the spec, so `Title` is a `string` and `Due` is a
`time.Time`, and the compiler holds the handler to the spec. `Collect` gathers the command
line, environment variables, configuration files and defaults into it in one documented
order, and validates it on the way. A bad value never reaches your code:

```
$ todo add "buy milk" --due tomorrow
Error: --due: "tomorrow" is not a valid date (write it as 2006-01-02)
```

That is the whole shape. Nothing is hidden in a base class or wired up behind your back; the
generated file is ordinary Go you can read, and the runtime is an ordinary import.

## Up and running

```bash
mkdir todo && cd todo && go mod init example.com/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go tool rotini init todo
go generate ./... && go build ./cmd/todo && ./todo --help
```

`rotini init` writes a working CLI — `--help`, `--version`, `help <command>` and `version`
already answer — in five files you can read in a few minutes. From there, growing the CLI is
the same loop every time: add to the spec, run `go generate`, fill in the new handler.

## Full-featured, one declaration at a time

The four files stay the same as the CLI grows; the spec just says more. Everything below is a
key in the spec, and none of it is wired unless you declare it:

- **every input channel** — flags, arguments, environment variables, configuration files
  found by path, by walking up from the working directory or under XDG, and typed stdin — with
  one precedence and a report of where each value came from;
- **structured values** — a flag that takes a whole object as JSON, `key=value` pairs, a file
  or one field per flag, validated against a schema;
- **composition** — a separate spec mounted as a sub-command, so several CLIs can ship alone
  and together;
- **plugins** — sub-commands that run another binary, declared or discovered on disk;
- **generated docs** — help, man pages, markdown and completion for bash, zsh, fish and
  PowerShell, all from the spec.

And for the handler side, the runtime carries what long-running and interactive tools need
beyond parsing: running subprocesses, daemon-style services with ordered shutdown, reading a
secret without echo, and running your whole command tree as a REPL.

The largest example, [rubectl](/examples/), is a thirty-command kubectl look-alike built on
all of it. It was written before rotini's API was frozen, precisely to find what a real CLI
of that size would trip over.

## The cost

rotini adds a codegen step: a tool dependency, a `go generate` pass, and generated files in
version control. For a three-command script, that is more than you need. For a tool that
will grow — more commands, configuration files, environment variables, docs and completion to
keep in step — the spec is the one place all of it is declared, and checked.

[Start with the setup guide](/docs/), or browse the [examples](/examples/).
