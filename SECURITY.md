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
   spec by reference — local, `mod://`, `git::`, or raw `https://`. A remote ref
   pulls authored intent across a trust boundary into your generated binary.
3. **The generated CLI's own runtime.** What the rotini runtime does — and
   deliberately does *not* do — when the end user's compiled program runs.

The package's job is to make the safe path the easy path: fetching is hermetic and
pinned, the runtime injects nothing you did not declare, and errors are built not
to leak secrets.

## Remote Spec Composition Is Pinned and Hermetic

External `$ref`s are resolved like dependencies, not fetched on the fly:

- **Fetching is confined to `rotini mod`.** `generate` and `validate` never reach
  the network — they read only the local lockfile and content-addressed cache. A
  code-generation pass is reproducible and offline.
- **`git::` and raw `https://` refs are locked.** `rotini mod` pins each to an
  immutable revision (a commit SHA for git) and the SHA-256 of the fetched bytes,
  recorded in `.rotini.lock` (a go.sum for specs). Codegen verifies fetched bytes
  against the lock and **refuses a moved tag or a tampered cache** rather than
  trusting them.
- **`mod://` refs ride Go's integrity.** A spec inside a Go module you depend on is
  read from the module cache; `go.mod`/`go.sum` are its pins, so Go's own
  verification applies. Local relative refs are pinned by the filesystem.

Review the contents of an external `$ref` before locking it, exactly as you would a
new module dependency.

## Tool / Library Version Compatibility

The rotini **tool** that generates code and the rotini **library** the consuming
module builds against must agree on the contract. `checkPackageVersion` refuses to
generate on a definite **cross-major mismatch** (a `go install`ed tool from a
different major than the project's `require`), catching the one case Go's minimal
version selection cannot. The check is conservative: it skips whenever it cannot
prove a mismatch (a dev/unknown tool version, a local `replace`, or no rotini
require), so it never blocks a legitimate build.

## Secret Handling

- **Secret-aware inputs.** Env/config inputs marked `secret` in the spec carry that
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
  flags, and no "did you mean" suggestions, unless you declare them in the spec.
  Fuzzy suggestion (`Suggestor`) ships, but is end-user opt-in.
- **No network during generation.** Only `rotini mod` fetches; `generate` and
  `validate` are offline.
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
- **Version compatibility is best-effort.** The cross-major guard intentionally
  errs toward allowing the build when it cannot prove a mismatch; it is a safety net,
  not a guarantee.

For the runtime contract, the outcome/error model, and the opt-in service registry,
see the package documentation in `doc.go`.
