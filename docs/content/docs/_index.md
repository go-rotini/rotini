---
title: "docs"
---

# Getting Started

## Quick start

From an empty directory to a CLI that runs, with a command tree and help pages.

{{< code title="quick start" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir todo && cd todo
go mod init github.com/me/todo

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest   # the generator
go get github.com/go-rotini/rotini@latest                    # the runtime

go tool rotini init todo
{{< /code >}}

{{< code title="what you get" language="text" open="true" collapsible="false" copy="false" >}}
$ go build ./cmd/todo && ./todo --help
TODO — the long description at the top of `todo --help`

Usage:
  todo <command> [flags]

Commands:
  help       print help
  version    print version

Flags:
  -h,--help       print help
  -v,--version    print version

Use "todo help <command>" for more information about a command.
{{< /code >}}

Open `cmd/todo/.rotini.spec.yaml`, add a command, run `go generate ./...`, and a handler stub is waiting for you. The rest of this page explains each of those steps; the [guides](/guides) pick up from there.

## Concepts

Three files and one loop. Everything else on this site is detail on one of them.

| | |
|---|---|
| **The spec** — `.rotini.spec.*` | your CLI as data: the command tree, and every input each command accepts. YAML, JSON, JSONC or TOML. |
| **The conf** — `.rotini.conf.*` | codegen settings: where generated code is written, which derived outputs (help, completion, man, markdown) are on. |
| **Your handlers** | ordinary Go, one file per command, seeded once and then yours. |

{{< code title="the loop" language="text" open="true" collapsible="false" copy="false" >}}
edit the spec  →  rotini validate  →  rotini generate  →  write the handler  →  go build
                  (schema + lints)     (types, wiring,
                                        help, stubs)
{{< /code >}}

Two ideas are worth holding onto before you read further:

- **rotini is commands all the way down.** The root command is the binary itself; a sub-command is the same object one level in. Every key that works on one works on the other.
- **Declared, then generated, then implemented.** A flag exists because the spec says so. The generator turns that into a typed field, a help line, a completion entry and a validation rule — so a handler receives values that are already parsed, coerced and checked, and never writes parsing code.

Nothing runs behind your back: rotini adds no flags you did not declare, detects nothing about the terminal, and wires no service you did not bind.

## 1. Set up the module

The rest of this page is the quick start, slowed down. Adding rotini to an existing project? Skip to step 2.

Create a new directory and initialize a Go module.

{{< code title="go mod init" language="text" open="true" collapsible="false" copy="true" >}}
mkdir todo
cd todo
go mod init github.com/me/todo
{{< /code >}}

## 2. Install rotini

rotini is one module with two faces. You need both: the **tool** generates your code, and the **runtime** is what that code imports.

{{< alert type="info" title="NOTE:" >}}
Because the tool and the runtime are the same module, `go get -tool` and `go get` resolve to a single `require` line at a single version — they cannot drift apart. rotini also checks the `version:` key in your spec and conf against the binary running `generate`, and refuses a mismatch rather than emitting code from a definition it does not understand.
{{< /alert >}}

### As a tool dependency <small>(recommended)</small>

A <cite>tool dependency[^1]</cite> records the rotini version in your `go.mod` under the `tool` directive, so every developer resolves the same CLI through the module graph. There is no separate installation step — `go tool` fetches and caches the binary.

{{< code title="go get" language="text" open="true" collapsible="false" copy="true" >}}
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest   # the tool
go get github.com/go-rotini/rotini@latest                    # the runtime
{{< /code >}}

{{< alert type="warning" title="THE TWO PATHS DIFFER:" >}}
The tool is the **command** at `.../rotini/cmd/rotini`; the runtime is the **module root**. `-tool` takes a package path and the root is a library, so `go get -tool github.com/go-rotini/rotini` fails with `not a main package`. Same module, same version, two package paths.
{{< /alert >}}

### As a global binary

Installing globally places the binary in your `GOBIN`. This suits prototyping across several projects, but the version is not tracked in any module graph, so each developer must keep their binary aligned with each project's `version:` key themselves.

{{< code title="go install" language="text" open="true" collapsible="false" copy="true" >}}
go install github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

## 3. Initialize the project

`init` scaffolds the spec, the conf, the entrypoint and a first handler stub, then runs the same `generate` every later pass runs.

{{< code title="rotini init" language="text" open="true" collapsible="false" copy="true" >}}
go tool rotini init todo
{{< /code >}}

Your project now contains:

- **`cmd/todo/.rotini.spec.yaml`** — the [specification](/specification): commands, flags, arguments, and every other input channel
- **`cmd/todo/.rotini.conf.yaml`** — the [configuration](/configuration): where code is written and which features are on
- **`cmd/todo/main.go`** — the entrypoint, carrying the `//go:generate` directive (create-once: never overwritten)
- **`internal/cmd/todo/`** — the [generated](/generated) framework file plus one editable handler stub per command

## 4. The development loop

Edit the spec, regenerate, build. The `//go:generate` directive in `main.go` means you never have to remember the command.

{{< code title="workflow" language="sh" open="true" collapsible="false" copy="true" >}}
# validate the spec without generating (fast; use it in CI)
go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml

# regenerate after a spec change
go generate ./...

# build and run
go build ./cmd/todo
./todo --help
{{< /code >}}

## 5. Write a handler

Each command gets one stub, created once and then yours. It implements the five lifecycle hooks; embed the `Default*` types for the ones you do not need.

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*todoAddHandlers)(nil)

type todoAddHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*todoAddHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	// One line reconciles every declared channel: argv, env, config files, stdin
	// and defaults, in the documented precedence order.
	inputs, err := rotini.Collect[TodoAddInputs](rtx)
	if err != nil {
		rtx.RecordError(err) // report it once, through the funnel
		rtx.Halt()           // and stop; the funnel picks the exit code
		return
	}

	fmt.Fprintln(rtx.Stdout, "added:", inputs.TodoAdd.Arguments.Title)
	rtx.RecordSuccess("task added")
}
{{< /code >}}

Two things worth noticing, because they are the conventions the rest of the docs assume:

- **Write to `rtx.Stdout`, never `os.Stdout`.** The streams come from the `Program`, so the same handler works under a test, a REPL, or a parent CLI that composed you.
- **Record, do not print, results and errors.** The runtime reports them once, after teardown, through one funnel — so a handler carries no reporting code and a program changes its reporting in one place.

`TodoAddInputs` is generated from the spec — you never declare it.

## Next

- [Guides](/guides) — adding commands and inputs, configuration, errors, testing, composition
- [Examples](/examples) — ten complete CLIs, and what each one shows
- [Specification](/specification) — every key of the spec file
- [API](/api) — what a handler is handed

[^1]: Tool directives were added in <a href="https://go.dev/doc/go1.24#tools" target="_blank">Go 1.24</a>
