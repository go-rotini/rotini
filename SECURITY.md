# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **matthewcgetz@gmail.com**. Do not open a public issue.

You should receive a response within 72 hours. If accepted, a fix will be developed privately and released as a patch version.

## Threat Model

Rotini is a spec-driven CLI **code generator** plus the **runtime library** the generated CLIs
import. There are three trust boundaries:

1. **A spec and conf as codegen input.** `rotini generate` turns a `.rotini.spec.*` (and
   `.rotini.conf.*`) into Go source that you compile and ship. Treat a spec you did not write
   with the same care as any dependency you vendor.
2. **Composed (`$ref`) specs from outside the repo.** A spec can include another spec by a local
   relative path, or by `mod://` from a Go module you already depend on. Either way, the
   referenced spec shapes your generated binary.
3. **The generated CLI's runtime.** What the rotini runtime does, and does not do, when your
   compiled program runs.

## Spec Composition Never Reaches the Network

Rotini has no fetcher. Every `$ref` resolves from something already on disk:

- **`generate` and `validate` are always offline.** They read only the filesystem and the Go
  module cache.
- **`git::` and raw `https://` refs are refused.** A spec naming one fails validation.
- **`mod://` refs use Go's integrity checks.** The spec is read from the module cache, so
  `go.mod` and `go.sum` pin it and Go's own verification applies. Local relative refs are pinned
  by the filesystem.

Review a composed `$ref` before depending on it, as you would a new module dependency.

## Tool and Library Versions

The rotini **tool** that generates code and the rotini **library** your module builds against
are one module, so `go tool rotini` runs at the version your `go.mod` requires. Every spec and
conf also declares a `version:`, the minimum rotini it was written for. `validate` and
`generate` reject a document that needs a newer rotini than the running tool, or that belongs
to a different major version.

## Secret Handling

- **Secret inputs are redacted.** An input marked `secret` in the spec has its value replaced
  with `[redacted]` in error messages.
- **Errors do not leak internals.** The typed errors (`ParseError`, `InputError`,
  `PluginError`, `WiringError`, `DependencyError`, `PanicError`, and the `ErrUsage` /
  `ErrInternal` sentinels) describe the failure without echoing secret values or exposing
  decoder or OS internals.

## What the Runtime Does Not Do

- **No telemetry.** The runtime emits no logs, metrics or network calls of its own.
- **No injected flags.** There are no implicit `--help`, `--version` or `--color` flags; declare
  the ones you want in the spec.
- **No "did you mean" suggestions by default.** A `ParseError` carries the rejected token and
  its candidates, and a program can rank them with `Suggestor` if its author chooses to.

The runtime has two built-in behaviors: it handles Ctrl+C and SIGTERM so the program shuts down
cleanly (turn this off with `WithoutSignalHandling`), and it answers a hidden `__complete`
command that the generated completion scripts call.

## Known Caveats

- **A spec is trusted input to codegen.** Rotini does not sandbox generation against a hostile
  spec; it generates the code the spec describes. Do not run `rotini generate` on a spec from an
  untrusted source you have not reviewed.
- **Composition across packages is checked at compile time.** A `$ref` that delegates to an
  external Go package is checked when you build, not by rotini.

For the runtime's error model and options, see the
[package documentation](https://pkg.go.dev/github.com/go-rotini/rotini).
