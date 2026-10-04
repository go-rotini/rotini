# Compatibility

What a rotini version number promises, and what it does not. rotini follows
[Semantic Versioning](https://semver.org): within a major version, everything under
**Covered** below keeps working without you changing anything.

rotini is unusual in having two faces — a library your code imports and a tool that writes
code into your repository — so "the API" is larger than a Go package's exported symbols. This
document is the full list.

## Supported Go versions

rotini requires the Go version in its `go.mod` (currently **Go 1.27**) and supports the two
most recent major Go releases. Raising the floor past that window is a **minor** release, not
a major one, following the same convention as the Go ecosystem at large.

## Covered by SemVer

### 1. The exported Go API

Every exported symbol of `github.com/go-rotini/rotini`: types, funcs, methods, constants,
sentinel errors, struct fields, and the signatures of all of them.

The snapshot in `testdata/surface.txt` is the authoritative list, frozen by
`TestExportedSurface` — a change to it fails the build until it is updated deliberately, so
every addition and removal appears in a diff a reviewer reads. It covers funcs, types, consts,
vars, methods, **struct fields** and **interface methods**: a removed field on `Definition` or
`FlagDef` breaks every generated file in every project, which is a larger break than removing
a function, not a smaller one.

Behavior is covered too, not just shape: the documented precedence order of the four competing
input channels (defaults < files < env < argv), the lifecycle hook order and its reverse unwind,
the outcome reporter's contract, and the exit-code floor. The stdin channel is the fifth and is not
in that order: it fills a command's declared payload field, which no other channel writes, so it
never competes for one.

Completing a rotini program from outside is covered: `Program.Complete` and
`Program.WithCompletion` hand a `CompletionFormat` the same `CompletionResult` (candidates,
descriptions, the spec's hint) that rotini's own completion computes. The built-in
`PluginCompletion` format's output is covered as behavior: it is the completion format kubectl,
Docker and Flux read (candidate lines, then a `:<directive>` line), and it follows those hosts,
not rotini. Only rotini's OWN format, the default `__complete` output
below, is private.

### 2. The spec and conf schema keys

Every key `internal/codegen/schema-spec.json` and `schema-conf.json` accept, and what it
means. A spec that validates against rotini 1.2 validates against rotini 1.9.

New keys are additive and arrive in minor releases. A key is never removed or given different
semantics within a major version. A key may be *deprecated* — it keeps working, and validation
warns.

The published copies at the module root (`schema-spec.json`, `schema-conf.json`) are byte
copies of those files, served from a release tag for editors that follow a `$schema` URL.

### 3. The generated code's public shape

What your handlers and your `main.go` are written against:

- the names of generated types (`<Prefix>Inputs`, `<Prefix>Flags`, `<Prefix>Arguments`,
  `<Prefix>Env`, `<Prefix>Config`, `<Prefix>Stdin`, `<Prefix>Output`) and their fields;
- the names of document-level named schemas, which become exported Go types;
- the exported package symbols codegen emits: `Program`, `Handlers()`, `ProgramHandlers`,
  `InputSettings`, the feature vars (`Help<Prefix>`, `Man<Prefix>`, `Markdown<Prefix>`,
  `Completion<Shell>`) and their resolvers (`Help()`, `Man()`, `Markdown()`, `Completion()`);
- the `Handler` interface every handler file implements, and the `No*` embeddable no-ops.

### 4. The CLI's own interface

`rotini init`, `rotini generate`, `rotini validate` — their arguments, flags, and exit codes.

## NOT covered by SemVer

These change in minor and patch releases. If you depend on one, pin the rotini version.

| | why |
|---|---|
| **The exact text of error and warning messages** | Improving a message is a bug fix. Branch on the typed error (`*ParseError`, `*InputError`, `*PluginError`, `*WiringError`, `*DependencyError`, `*PanicError`), on `ParseError.Kind`, or on `CategoryOf` — never on a string. |
| **The layout of generated help, man and markdown pages** | Column widths, wrapping, spacing and section ordering are presentation, and they improve. If you golden-test your CLI's `--help`, expect to re-record it on a minor upgrade — or take ownership of the layout by turning the feature's `template` knob on, which is exactly what it is for. |
| **Generated file formatting** | Comment wording, import grouping, blank lines. The *identifiers* are covered (§3); the whitespace around them is not. |
| **The contents of a newly seeded handler stub** | `rotini init` and the first `generate` write starter code. It is yours from the moment it is written and rotini never overwrites it (it only removes it if its command leaves the spec), so what a *future* rotini would have seeded is not a compatibility surface. |
| **Anything under `internal/`** | Not importable, by Go's own rule. |
| **The `__complete` wire protocol** | Private between a generated completion script and the binary that ships with it. Both come from the same `generate`, so they cannot disagree. A host that completes a rotini program from outside uses `Program.Complete` or `Program.WithCompletion` with a `CompletionFormat`, which are covered. |
| **Benchmark numbers** | Tracked, not promised. |

## The version guard

Your spec and conf each declare a `version:`. It is a **minimum**, not a pin:

| document `version:` | rotini binary | result |
|---|---|---|
| `1.2.0` | `1.2.0` | ok |
| `1.2.0` | `1.4.1` | ok — newer, same major |
| `1.4.0` | `1.2.0` | **error** — the binary may not know keys the document uses |
| `1.x` | `2.x` | **error** — different major |
| any | unstamped dev build | skipped |

So taking a patch or minor release never requires editing a single spec or conf file. You
raise `version:` when you start using a key that needs a newer rotini — validation tells you
which, and when.

## Deprecation policy

A covered thing is removed only in a major release, and only after it has been deprecated for
at least one minor release before that:

- a Go symbol gets a `// Deprecated:` doc comment naming the replacement;
- a schema key keeps working and `rotini validate` emits a non-fatal warning;
- both are listed in the release notes of the version that deprecated them.

## Reporting a break

If a rotini upgrade breaks something listed under **Covered**, that is a bug, not a migration.
Open an issue with the before/after and the two versions; it will be treated as a regression.
