# go-rotini/rotini

**Declare your CLI in a spec file. rotini checks it, generates the typed Go, and
ships the rest of the binary.**

`rotini` is two-faced, and one module serves both faces at one version:

- **As a tool** — `go get -tool github.com/go-rotini/rotini/cmd/rotini` — it installs the
  codegen binary: `go tool rotini init`, `generate`, `validate`.
- **As a library** — `go get github.com/go-rotini/rotini` — it is the runtime your
  generated code imports and your handlers are written against.

Because both come from the same module, the tool and the runtime **cannot drift**.

```yaml
# cmd/todo/.rotini.spec.yaml
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
```

`go generate ./...` turns that into a typed `TodoAddInputs` struct, the command tree,
and an editable handler stub. You fill in the body.

## Three things that are actually different

**1. Your CLI is checked before your code exists.** The spec is validated by a JSON
Schema plus 34 rotini lint rules — a misspelled key, a duplicate flag identifier, a
config file nothing reads, a `$ref` cycle, an input whose type is not a Go type. Each
is reported with a `file:line:col`, by `rotini validate`, before a line of Go is
generated. Frameworks that declare the CLI *in Go* can only catch what the compiler
happens to notice.

**2. One import is the whole binary, not just its front door.** Parsing is the first
10% of a CLI. rotini also carries the other 90% — and every piece is opt-in, wired only
because your handler constructed it:

| | |
|---|---|
| `Printer` | render a result as text/JSON/YAML/TOML/table off your `--output` flag |
| `Table` | aligned columns, measured by display width (styling and wide runes align) |
| `Prompt` `Confirm` `Select` | ask questions; a pipe or CI gets `ErrNotInteractive`, never a hang |
| `Spinner` `Progress` | live one-line indicators, silent on a non-terminal |
| `Pager` | `$PAGER`, passing straight through when piped |
| `Subprocess` | `exec` with env/dir/timeout; streams output as an iterator |
| `REPL` | run your command tree as an interactive loop |
| `Service` `Scheduler` | daemon workers and interval tasks with graceful shutdown |
| `StdioServer` | JSON-RPC 2.0 over stdio — LSP and MCP framing |
| `Wizard` | multi-step flows with branching and back navigation |

Two things rotini deliberately does **not** reimplement, because they already exist in
the same ecosystem: **file watching** is `fs.NewWatcher` and a **single-instance lock**
is `fs.PIDLock`, both in [`go-rotini/fs`](https://github.com/go-rotini/fs); caching for
a long-running program is [`go-rotini/memcache`](https://github.com/go-rotini/memcache).
rotini does not wrap them — a facade would put another package's API inside rotini's
frozen surface and put its documentation in the wrong place.

**3. The generated code is small, legible, and yours.** `rotini init` generates
**376 lines across 5 files** — and they are a CLI that already answers `--help`,
`--version`, `help <command>` and `version`, because the seeded spec declares them and
the seeded handlers are wired to the pages codegen just produced. Nothing is injected at
run time; every line is in your repo, and every line is yours to delete. The machinery is
an ordinary import you upgrade with `go get -u` — not a vendored copy you must never edit.

## The cost, stated up front

rotini adds a **codegen step**: a tool dependency, a `go generate` pass, and generated
files in version control. Frameworks driven by struct tags ask for none of that.

That cost is fixed; the benefits scale with the CLI. For a three-command internal
script, rotini is heavier than it is worth. For a long-lived, multi-command tool with
config files, environment variables, docs and shell completion to keep in sync, the
spec becomes the single place all of it is declared — and checked.

## Install

Requires **Go 1.27** or later.

```
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest   # the codegen tool
go get github.com/go-rotini/rotini@latest                    # the runtime
```

(Your own module may still declare an older Go version — calling rotini's generic
methods does not require 1.27 in the caller, only declaring them does.)

## Quickstart

```bash
mkdir todo && cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest

go tool rotini init todo
# Scaffolds:
#   cmd/todo/.rotini.spec.yaml   — declare commands / flags / arguments here
#   cmd/todo/.rotini.conf.yaml   — codegen settings (packages, features)
#   cmd/todo/main.go             — entrypoint (carries the //go:generate directive)
#   internal/cmd/todo/           — generated framework + one handler stub per command

go get github.com/go-rotini/rotini   # the runtime the generated code imports

# grow the CLI by editing the spec, then regenerate:
go generate ./...                # re-runs `rotini generate` via the directive in main.go

# write your handler bodies in internal/cmd/todo/*.go, then build:
go build ./cmd/todo
./todo --help
```

## Commands

| Command      | Aliases | Purpose                                                         |
|--------------|---------|-----------------------------------------------------------------|
| `initialize` | `init`  | Scaffold a new CLI (spec + conf + entrypoint + first generate). |
| `generate`   | `gen`   | Generate the framework/wiring from a spec + conf.               |
| `validate`   | `val`   | Validate a spec + conf without generating.                      |
| `help`       |         | Help for any command.                                           |
| `version`    |         | Print the tool version.                                         |

## What you declare in a spec

Every way data reaches your CLI is declared, not wired by hand:

- **argv** — flags (typed, clustering, count, repeatable, groups, dependencies) and
  positional arguments (variadic, passthrough)
- **environment** — explicit variables, an `env_prefix`, or nested families
- **configuration files** — a fixed path, a walk-up search, XDG, or a path supplied at
  run time by a flag or env var (`config_source`)
- **stdin** — a typed, schema-validated payload, or the `-` and `@file` value sentinels
- **defaults**, with a documented precedence chain and per-field provenance

`rotini.Collect[T](rtx)` reconciles all of it in one line.

## Documentation

- **Runtime contract** — `Program`/`Execute`, the lifecycle hooks, the outcome funnel
  and typed error taxonomy, and the opt-in services: [`doc.go`](doc.go).
- **Exhaustive, annotated schema references**:
  [`reference/.rotini.spec.yaml`](reference/.rotini.spec.yaml) and
  [`reference/.rotini.conf.yaml`](reference/.rotini.conf.yaml). Both are validated by
  the test suite, so they cannot drift from the schemas.
- **Editor support** — point your spec's `$schema` at a released schema and every key is
  completed and checked as you type:

  ```yaml
  $schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/v1.0.0/schema-spec.json
  ```

  Offline or forked? `generate.schemas` in the conf writes a local copy to point at instead.
- **What a version number promises** — [`COMPATIBILITY.md`](COMPATIBILITY.md).
- **Taking a new version** — [`UPGRADING.md`](UPGRADING.md).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). For vulnerability reports, see
[`SECURITY.md`](SECURITY.md).

## License

MIT. See [`LICENSE`](LICENSE).
