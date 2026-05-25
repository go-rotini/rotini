---
title: "docs"
---

# Setup

This guide walks through creating a new Go project with rotini. If you are adding rotini to an existing project, skip to step 2.

## 1. Go Module Setup

Create a new directory and initialize a Go module.

{{< code title="go mod init" language="text" open="true" collapsible="false" copy="true" >}}
mkdir mypkg
cd mypkg
go mod init mypkg
{{< /code >}}

## 2. Install Rotini

Rotini can be installed as either a project-scoped tool dependency or a globally-installed binary. Both methods produce the same CLI — the difference is how the version is resolved and whether it is tied to your module.

{{< alert type="info" title="NOTE:" >}}
Rotini enforces strict version alignment between the CLI and your spec file. The rotini binary version must match the `$schema` field declared in your rotini specification and configuration files exactly. If there is a mismatch, `rotini generate` will exit with an error. This guard prevents generated code from quiet divergence and loud breakage.
{{< /alert >}}

### a. Tool Dependency <small>(recommended)</small>

Using a <cite>tool dependency[^1]</cite> records the rotini version directly in your `go.mod` under the `tool` directive. This ensures that every developer on the project resolves the same CLI version through the module graph, eliminating version skew across environments. There is no separate installation step — `go tool` fetches and caches the binary automatically.

{{< code title="go get -tool" language="text" open="true" collapsible="false" copy="true" >}}
go get -tool github.com/go-rotini/rotini@latest
{{< /code >}}

### b. Global Install

Installing rotini globally places the binary in your `GOBIN` directory. This approach is straightforward for prototyping or evaluating rotini across multiple projects, but the installed version is not tracked in your module graph. In a collaborative setting, each developer must independently ensure their installed version matches the project's `$schema` — making version drift a likely source of errors.

{{< code title="go install" language="text" open="true" collapsible="false" copy="true" >}}
go install github.com/go-rotini/rotini@latest
{{< /code >}}

## 3. Initialize Your Project

The `init` command scaffolds the rotini spec file (`.rotini.yaml`) and initial handler stubs for your project. It takes your Go package name as an argument.

{{< code title="go tool" language="text" open="true" collapsible="false" copy="true" >}}
go tool github.com/go-rotini/rotini@latest init mypkg
{{< /code >}}

{{< code title="go install" language="text" open="true" collapsible="false" copy="true" >}}
rotini init mypkg
{{< /code >}}

After running `init`, your project will contain:

- **`.rotini.yaml`** — the spec file that defines your CLI's commands, flags, and arguments
- **`internal/cmd/`** — handler stubs where you implement your command logic
- **`main.go`** — the entry point that wires everything together

## 4. Local Development

Rotini uses `go generate` to produce typed Go source from your spec file. The typical development loop is: edit the spec, generate, build, and test.

{{< code title="workflow" language="sh" open="true" collapsible="true" copy="true" >}}
# run
go generate ./...
go run .

# OR

# build
go generate ./...
go build -o ./mypkg .
./mypkg

# OR

# install
go generate ./...
go install .
mypkg
{{< /code >}}

{{< code title="go example" language="golang" open="true" collapsible="true" copy="true" >}}
package cmd

import (
  "context"

  "github.com/go-rotini/rotini/internal/rotini"
)

type rotiniHandlers struct{}

var _ rotini.RotiniHandlers = (*rotiniHandlers)(nil)

func (*rotiniHandlers) CascadingPreRun(ctx context.Context, rtx rotini.RotiniCtx) {
}

func (*rotiniHandlers) PreRun(ctx context.Context, rtx rotini.RotiniCtx) {
}

func (*rotiniHandlers) Run(ctx context.Context, rtx rotini.RotiniCtx) {
}

func (*rotiniHandlers) PostRun(ctx context.Context, rtx rotini.RotiniCtx) {
}

func (*rotiniHandlers) CascadingPostRun(ctx context.Context, rtx rotini.RotiniCtx) {
}
{{< /code >}}

[^1]: Tool directives were added in <a href="https://go.dev/doc/go1.24#tools" target="_blank">Go 1.24</a>
