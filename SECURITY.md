# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **matthewcgetz@gmail.com**. Do not open a public issue.

You should receive a response within 72 hours. If accepted, a fix will be developed privately and released as a patch version.

## Threat Model

`rotini` is a spec-driven CLI **code generator** plus the **runtime library** the
generated CLIs import. Its security posture is shaped around three trust boundaries:

1. **A spec/conf as codegen input.** `rotini generate` turns a `.rotini.spec.*`
   (and `.rotini.conf.*`) into Go source that you then compile and ship. A spec is
   therefore code-adjacent: treat one you did not author with the same care as any
   dependency you vendor.
2. **Composed (`$ref`) specs from outside the repo.** A spec can compose another
   spec by reference — a local relative path, or `mod://` for a spec inside a Go
   module you already depend on. Either way the ref pulls authored intent across a
   trust boundary into your generated binary.
3. **The generated CLI's own runtime.** What the rotini runtime does — and
   deliberately does *not* do — when the end user's compiled program runs.

The package's job is to make the safe path the easy path: codegen never reaches the
network, the runtime injects nothing you did not declare, and errors are built not
to leak secrets.

## Spec Composition Never Reaches the Network

`rotini` has no fetcher. Every `$ref` resolves from something already on disk:

- **`generate` and `validate` are offline, always.** There is no command that
  fetches a spec, so a code-generation pass cannot be influenced by the network.
- **`git::` and raw `https://` refs are REFUSED.** rotini neither fetches nor pins
  them; a spec naming one fails validation rather than being retrieved. (Earlier
  designs pinned them through a `rotini mod` command and a `.rotini.lock` file;
  both were dropped in favor of not fetching at all.)
- **`mod://` refs ride Go's integrity.** A spec inside a Go module you depend on is
  read from the module cache, so `go.mod`/`go.sum` are its pins and Go's own
  verification applies. Local relative refs are pinned by the filesystem.

Review the contents of a composed `$ref` before depending on it, exactly as you
would a new module dependency.

## Tool / Library Version Compatibility

The rotini **tool** that generates code and the rotini **library** the consuming
module builds against are one module, so `go tool rotini` runs at the version your
`go.mod` requires. On top of that, every spec and conf declares a `version:`: the
minimum rotini it was written against. `validate` and `generate` reject a document
that needs a newer rotini than the running tool, or that belongs to a different
major version, so generated code never quietly diverges from the definition it came
from.

## Secret Handling

- **Secret-aware inputs.** Inputs marked `secret` in the spec carry that
  marking through to the generated binding, so secret values are handled distinctly
  from ordinary inputs.
- **Errors do not leak secrets.** The typed error and fault classes
  (`ParseError`, `BindError`, `RemoteError`, `WiringError`, `ServiceError`,
  `PanicError`, and the `ErrUsage` / `ErrInternal` sentinels) are designed to be
  non-leaky: messages describe the failure without echoing secret input values, and
  internal faults surface as `ErrInternal` rather than spilling internals.

## What the Runtime Does NOT Do

Pillar 1 of rotini's design is that the runtime injects nothing you did not ask for:

- **No telemetry.** The runtime emits no logs, metrics, or network calls of its own.
- **No auto-injected behavior.** No implicit `--help`/`--version`/`--color`/`--no-*`
  flags; declare the ones you want in the spec.
- **No "did you mean" suggestions by default.** A program built with rotini prints
  none. A `ParseError` carries the rejected token and its candidates, and a program may
  rank them with `Suggestor` — the program author's choice, never rotini's default. The
  `rotini` tool itself makes that choice, as any program can.
- **No network during generation.** rotini has no fetcher at all: `generate` and
  `validate` read only the filesystem and the Go module cache.
- **One runtime default.** Interrupt/SIGTERM handling is on by default (so a CLI
  shuts down cleanly on Ctrl-C); it is opt-out via `WithoutSignalHandling`. Every
  other runtime service is something a handler explicitly fetches or binds.

## Known Caveats

- **A spec is trusted input to codegen.** `rotini` does not sandbox generation
  against a hostile spec; it generates the code the spec describes. Do not run
  `rotini generate` against a spec from an untrusted source you have not reviewed.
- **The composition contract is enforced at compile time.** A `$ref` that delegates
  to an external Go package is checked when you build, not by rotini — rotini cannot
  type-check a foreign package on your behalf.

For the runtime contract, the outcome/error model, and the opt-in service registry,
see the package documentation in `doc.go`.
