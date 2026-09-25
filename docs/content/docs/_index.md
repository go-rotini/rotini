---
title: "docs"
---

# Setup

This guide walks through creating a new Go project with rotini. If you are adding rotini to an existing project, skip to step 2.

## 1. Set up the module

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
		rtx.RecordError(err)
		return
	}

	fmt.Fprintln(rtx.Stdout, "added:", inputs.TodoAdd.Arguments.Title)
	rtx.RecordSuccess("task added")
}
{{< /code >}}

`TodoAddInputs` is generated from the spec — you never declare it. A handler does not print its own errors: it **records** them, and the runtime reports them once, after teardown. See [api](/api).

[^1]: Tool directives were added in <a href="https://go.dev/doc/go1.24#tools" target="_blank">Go 1.24</a>
