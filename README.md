# go-rotini/rotini

A spec-driven CLI code generator for Go, plus the single `rotini` runtime
package that generated CLIs import.

`rotini` is two-faced:

- **As a tool** (`go get -tool github.com/go-rotini/rotini`) it exposes the
  codegen binary. You invoke `go tool rotini init`, `go tool rotini generate`,
  `go tool rotini validate`, `go tool rotini mod`, etc.
- **As a library** (`go get github.com/go-rotini/rotini`) it exposes the
  `rotini` package — the slim runtime the generated entrypoint builds a
  [`Program`] with and calls `Execute()` on.

You describe your CLI's commands, flags, and arguments in a `.rotini.spec.*`
file; `rotini generate` emits the typed framework and wiring; you fill in the
handler bodies. The runtime injects nothing you didn't declare (no implicit
`--help`/`--version`/`--color`, no "did you mean") — every convenience is opt-in.

## Install

```
go get -tool github.com/go-rotini/rotini@latest
```

## Quickstart

```bash
mkdir todo && cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini@latest

go tool rotini init todo
# Scaffolds:
#   cmd/todo/.rotini.spec.yaml   — declare commands / flags / arguments here
#   cmd/todo/.rotini.conf.yaml   — codegen settings (packages, features)
#   cmd/todo/main.go             — entrypoint (carries the //go:generate directive)
#   internal/cmd/todo/           — generated framework + one empty handler stub per command

# edit cmd/todo/.rotini.spec.yaml to grow your CLI, then regenerate:
go generate ./...                # re-runs `rotini generate` via the directive in main.go

# write your handler bodies in internal/cmd/todo/*.go, then build:
go build ./cmd/todo
./todo --help
```

## Commands

| Command      | Aliases | Purpose                                                          |
|--------------|---------|------------------------------------------------------------------|
| `initialize` | `init`  | Scaffold a new CLI (spec + conf + entrypoint + first generate).  |
| `generate`   | `gen`   | Generate the framework/wiring from a spec + conf.                |
| `validate`   |         | Validate a spec + conf without generating.                       |
| `mod`        |         | Fetch and pin external `$ref` specs into `.rotini.lock`.         |
| `completion` |         | Emit a shell completion script.                                  |
| `help`       |         | Help for any command.                                            |
| `version`    |         | Print the tool version.                                          |

## Documentation

- The runtime contract — `Program`/`Execute`, the outcome and typed-error model,
  and the opt-in service registry — lives in the package documentation
  ([`doc.go`](doc.go)).
- Exhaustive, annotated schema references for the spec and conf files:
  [`.docs/.rotini.spec.yaml`](../.docs/.rotini.spec.yaml) and
  [`.docs/.rotini.conf.yaml`](../.docs/.rotini.conf.yaml).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). For vulnerability reports, see
[`SECURITY.md`](SECURITY.md).

## License

MIT. See [`LICENSE`](LICENSE).
