# go-rotini/rotini

A spec-driven CLI code generator for Go, plus the `rtk` sub-package holding
the rotini-runtime toolkit (parser, IO, OS, signals, ticker, registry, ctx —
and over time term, output, prompt, progress, exec, httpx, daemon).

`rotini` is two-faced:

- **As a tool** (`go get -tool github.com/go-rotini/rotini`) it exposes the
  codegen binary. Users invoke `go tool rotini init`, `go tool rotini
  generate`, etc.
- **As a library** (`go get github.com/go-rotini/rotini`) it exposes the
  single `rtk` sub-package for generated CLIs to import.

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
# edit .rotini.spec.yaml to declare commands / flags / arguments
go tool rotini generate
# edit internal/handlers/*.go to write handler bodies
go build .
./todo --help
```

For the end-to-end walkthrough, the full package contract, the spec format
reference, and the implementation plan, see
[`.docs/ROTINI_PACKAGE_REQUIREMENTS.md`](../.docs/ROTINI_PACKAGE_REQUIREMENTS.md).

For the historical archive of superseded planning docs, see
[`.docs/ROTINI_PACKAGE_CONTEXT.md`](../.docs/ROTINI_PACKAGE_CONTEXT.md).

## License

MIT. See [`LICENSE`](LICENSE).
