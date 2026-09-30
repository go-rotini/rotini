# go-rotini/rotini

**Declare your CLI in a spec file. rotini checks it, generates the typed Go, and ships the
runtime your handlers are written against.**

One module, two faces, one version — so the tool and the runtime cannot drift:

- **the tool** — `go get -tool github.com/go-rotini/rotini/cmd/rotini` — `rotini init`,
  `generate` and `validate`;
- **the library** — `go get github.com/go-rotini/rotini` — the runtime the generated code
  imports.

## The whole idea, in one example

```yaml
# cmd/todo/.rotini.spec.yaml
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

`rotini validate` checks that spec against a JSON Schema and 43 lint rules, each problem
reported with a `file:line:col`. `go generate ./...` turns it into a typed `TodoAddInputs`
struct, the command tree, help pages, and a handler stub that already answers `--help`. The
part you write is the command itself:

```go
in, err := rotini.Collect[TodoAddInputs](rtx) // argv, env, config and defaults, validated
if err != nil {
	rtx.HaltWith(err)
	return
}
fmt.Fprintf(rtx.Stdout, "added %q, due %s\n", in.TodoAdd.Arguments.Title, in.TodoAdd.Flags.Due.Format(time.DateOnly))
```

A rotini CLI is four kinds of file, each easy to reason about on its own: the **spec** (what
the CLI accepts), the **conf** (where the generated code goes), **`main.go`** (the entrypoint),
and one **handler file** per command (what it does). `rotini init` writes a working CLI —
**398 lines across 5 files**, already answering `--help`, `--version`, `help` and `version`
— and every line of it is yours to read and change.

## The cost, stated up front

rotini adds a **codegen step**: a tool dependency, a `go generate` pass, and generated files
in version control. That cost is fixed while the benefit grows with the CLI: for a
three-command script it is more than you need; for a long-lived tool with configuration
files, environment variables, docs and shell completion to keep in step, the spec is the one
place all of it is declared — and checked.

## Install

Requires **Go 1.27** or later.

```bash
mkdir todo && cd todo && go mod init example.com/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go tool rotini init todo
go generate ./... && go build ./cmd/todo && ./todo --help
```

## Learn more

- **[rotini.dev](https://rotini.dev)** — the guide: setup, the spec language, worked
  examples, and the [spec](https://rotini.dev/specification/reference/) and
  [conf](https://rotini.dev/configuration/reference/) references.
- **[pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini)** — the runtime API.
- **[COMPATIBILITY.md](COMPATIBILITY.md)** — what a version number promises;
  **[UPGRADING.md](UPGRADING.md)** — taking a new one.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** · **[SECURITY.md](SECURITY.md)** · MIT
  **[LICENSE](LICENSE)**.
