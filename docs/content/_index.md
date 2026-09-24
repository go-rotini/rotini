---
title: "rotini"
---

<div class="rotini_text">rotini</div>

**Declare your CLI in a spec file. rotini checks it, generates the typed Go, and ships the rest of the binary.**

rotini is two-faced, and one module serves both faces at one version: a **codegen tool** you install with `go get -tool`, and the **runtime** your generated code imports with `go get`. Because both come from the same module, the tool and the runtime cannot drift.

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0
command:
  name: todo
  description: a task list
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
{{< /code >}}

`go generate ./...` turns that into a typed `TodoAddInputs` struct, the command tree, and one editable handler stub. You fill in the body.

## Three things that are actually different

### 1. Your CLI is checked before your code exists

The spec is validated by a JSON Schema plus 34 rotini lint rules — a misspelled key, a duplicate flag identifier, a configuration file nothing reads, a `$ref` cycle, an input whose type is not a Go type. Each is reported with a `file:line:col`, by `rotini validate`, before a line of Go is generated.

A framework that declares the CLI *in Go* can only catch what the compiler happens to notice.

### 2. One import is the whole binary, not just its front door

Parsing is the first 10% of a CLI. rotini also carries the other 90% — rendering results, prompting, progress, paging, shelling out, and running as a REPL, a daemon, or an MCP server. Every piece is opt-in: importing rotini wires none of them, starts no goroutine, and touches no terminal.

See [batteries](/batteries).

### 3. The generated code is small, legible, and yours

`rotini init` generates **356 lines across 5 files** — and those five files are a CLI that already answers `--help`, `--version`, `help <command>` and `version`, because the seeded spec declares them and the seeded handlers are wired to the pages codegen just produced. Readable in one sitting, reviewable in a diff, and every line of it yours to delete.

The machinery is an ordinary import you upgrade with `go get -u`, not a vendored copy you must never edit.

## The cost, stated up front

rotini adds a **codegen step**: a tool dependency, a `go generate` pass, and generated files in version control. Frameworks driven by struct tags ask for none of that.

That cost is fixed; the benefit scales with the CLI. For a three-command internal script, rotini is heavier than it is worth. For a long-lived, multi-command tool with configuration files, environment variables, documentation and shell completion to keep in sync, the spec becomes the single place all of it is declared — and checked.

## Start here

- [docs](/docs) — set up a project, end to end
- [specification](/specification) — the `.rotini.spec.*` file
- [configuration](/configuration) — the `.rotini.conf.*` file
- [generated](/generated) — what rotini writes into your project
- [batteries](/batteries) — everything past parsing
- [api](/api) — the runtime contract
- [cli](/cli) — the `rotini` command itself
