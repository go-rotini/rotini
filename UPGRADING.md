# Upgrading rotini

Upgrading is: take the new version, regenerate, read the diff, build. This document says what
to expect at each step, and what rotini will and will not touch in your repository.

See [COMPATIBILITY.md](COMPATIBILITY.md) for what a version number promises.

## The short version

```bash
go get -u github.com/go-rotini/rotini                      # the runtime
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest # the tool — same module, same version
go generate ./...                                          # regenerate
git diff                                                   # read it
go build ./... && go test ./...
```

Taking a patch or minor release does **not** require editing your spec or conf: the `version:`
key in each is a minimum, not a pin (see COMPATIBILITY.md). If validation complains that the
binary is older than your document, you upgraded the runtime and not the tool, or the other
way round — they are one module and must be at one version.

The version `rotini version` reports comes from the binary's **build info**, which through
`go tool` is the version in your `go.mod`. So the tool version and your `require` line are the
same fact, and there is nothing separate to keep aligned. (A build from source may stamp one
in with `-ldflags "-X main.version=…"`; that applies only when build info carries no release
version — a development build or a pseudo-version — because a real module version is the
better answer.)

## What regenerating touches

This is the part worth knowing precisely, because it is the only operation that can destroy
work.

| file | on regenerate |
|---|---|
| the generated file (`zz_rotini.go` unless `packages[type=cmd].file` says otherwise) | **rewritten every time.** Never edit it; the header says so. |
| the models file, when `packages[type=models]` is declared | **rewritten every time.** |
| a handler file (`<root>_<path>.go`) | **created once, never overwritten.** Your edits are safe — but it is removed if its command leaves the spec; see Pruning. |
| `main.go` (`packages[type=main].file`) | **created once, then never touched.** It holds your build metadata and your service bindings. |
| an editable template under `template_dir` | **seeded once when missing, then never touched.** |
| rendered output under `embed_dir` (`help_*.txt`, `man_*.txt`, `markdown_*.md`, `completion_*.txt`) | rewritten, and pruned per feature. |
| the schema files named by `generate.schemas` | overwritten from the embedded copies, never pruned. |
| anything else in the cmd package | **left alone.** Pruning removes only files rotini wrote — see below. |

### Pruning

The generator prunes handler files in the cmd package that no longer correspond to a command
in the spec. That is how renaming or deleting a command cleans up after itself.

**Only files rotini wrote are candidates.** A generated handler file carries a marker —
`var _ rotini.Handlers = (*xHandlers)(nil)` — and that marker is what makes it prunable. It
survives your edits, so an edited handler file is still pruned when its command goes. A helper
you put beside your handlers is never touched, whatever it is named. To keep a handler file
whose command is gone, delete its marker line or list it under `keep:`. Every prune is
reported:

```
Warning: pruned demo_ship.go — its command is no longer in the spec
```

What can still surprise you:

- **Renaming a command** (or changing its `filename:`) orphans the old handler file.
  Regenerating deletes it and seeds a new, empty one. **Move your handler body first**, or recover it from
  git afterwards.
- **Deleting a command** deletes its handler file, which is usually what you wanted.

Anything the pruner must spare goes in that package's `keep:` list. Test files and seeded
templates are kept automatically.

`rotini generate` is idempotent, so the safe way to find out what an upgrade will do is to run
it on a clean working tree and read `git diff`. Nothing is written outside the directories
your conf names.

## Reading the diff

Expect, in a minor upgrade:

- **changes inside the generated file** — new glue, a reordered literal, a new helper. Covered
  API keeps its names (COMPATIBILITY.md §3), so your handlers keep compiling.
- **changes to rendered help/man/markdown** — layout is not covered by SemVer. If you
  golden-test your own `--help`, re-record it. If you would rather own the layout, turn the
  feature's `template: true` knob on: the template is seeded into your repo once and rotini
  renders from your copy afterwards.
- **no changes to your handler files or `main.go`.** If you see one, something is wrong — file it.

## Upgrading across a major version

A major release may remove things that were deprecated in the release line before it. The
sequence:

1. Upgrade to the **latest minor of your current major** first and run `rotini validate`.
   Deprecations surface as non-fatal warnings there, naming each replacement. Fix them while
   still on a version that accepts both spellings.
2. Re-run `go build ./...` and address any `// Deprecated:` symbols the compiler or your
   linter flags.
3. Only then move to the new major and regenerate.

Raising `version:` in your spec and conf is the last step, not the first: it declares the
minimum rotini your documents need, so raise it once you actually use something new.

## Pre-v1 API changes

Before v1.0 the surface was still being shaped, and a rename landed as a rename rather than as
a deprecation cycle. Each one is mechanical; the compiler finds every call site.

| Was | Is | Why |
|---|---|---|
| `Parser.Deprecations(rtx)` | `rotini.Deprecations(rtx)` | it never used its receiver, so it forced a `*Parser` out of the registry — which in turn made `Bind(KeyParser, …)` look mandatory in every entrypoint. The seeded `main.go` no longer binds a parser; bind one only to override the default or to call `Parser.Parse` yourself |
| `Program.WithPanicForward(bool)` | `Program.WithTeardownOnPanic(bool)` | it names whether **teardown** runs, not where the panic goes. "Forward" read as forwarding the panic onward, which is what `WithPanicRecover(false)` actually does — a name that had to be unlearned from its own doc, on an option people reach for mid-crash |
| `Context.RecordErr(err)` | `Context.RecordError(err)` | it matches its siblings `RecordWarning`, `RecordInfo` and `RecordSuccess` |
| the per-outcome setters `WithOnSuccessFn` · `WithOnWarningFn` · `WithOnErrorFn` · `WithOnPanicFn` | one `Program.WithFunnel(FunnelFunc)`, which receives every outcome in one `Outcome` | one function owns the whole report and the exit code, so the order and the exit policy live in one place |
| the six `Key*` constants — `KeyVersion` `KeyParser` `KeyBinder` `KeyBindMeta` `KeyStyler` `KeySuggestor` | `Program.WithVersion` · `WithParser` · `WithBinder` · `WithBindMeta`, read back with `Context.Version()` · `Parser()`. The styler is gone; a `Suggestor` is a plain value from `rotini.NewSuggestor()` that a program uses in its own funnel | rotini's internals shared one flat, unreserved string namespace with your own services, and every read discarded its comma-ok — so a colliding name or a wrong type silently produced a zero value. Binding a `Binder` built from an empty `BindMeta`, the obvious way to write it, switched the configuration-file channel off without a word. The registry is now yours alone |

`WithBinder` takes `func(BindMeta) *Binder` rather than a `*Binder`: an override **receives**
the generated descriptor instead of having to reproduce it, which makes the silent-drop
mistake unwritable.

A **composed `mod://` child must be regenerated too** — its generated `NewProgram` calls
`WithBindMeta` now. Regenerate the published module and bump the `$ref`.

Also **added**, so nothing breaks: `rotini.Provide(key, value)` returning a `rotini.Option`,
and `Program.With(opts ...Option)`, which let several type-checked binds sit in one chain.
`Key.Provide` is unchanged and still the better call for a single service.

A **new wiring fault**: a command with `config:` inputs bound by a program that never called
`WithBindMeta` is now reported instead of silently filling every configuration value with its
zero. Supplying an *empty* `BindMeta` stays legal — "this program has no configuration
sources" is a choice, and telling it apart from "nobody wired the descriptor" is only possible
now that the descriptor is a typed option rather than a registry entry.

## Upgrading a composed tree

A spec that composes others (`$ref`) has more than one document to keep in step:

- **local `$ref`** — sibling specs in the same module upgrade with it; regenerate from the
  root and every composed child is rebuilt.
- **`mod://` `$ref`** — the child comes from the module cache, pinned by `go.sum`. Upgrading
  rotini does not move it. Bump the child module separately, then regenerate; its own
  `version:` guard fires if it needs a newer rotini than you have.
- **`remote_commands` / `remote_discovery`** — sibling binaries are dispatched at run time, not
  composed. Nothing to regenerate; rebuild each binary on its own schedule.

## Downgrading

Supported within a major version, with one rule: a document whose `version:` is newer than the
binary is rejected, by design — the older tool may not know the keys the document uses. Lower
`version:` to the binary's version after confirming the document does not use anything newer.
Validation names the key if it does.

## If an upgrade breaks something

1. `git diff` on a clean tree — the change is almost always visible in the generated file.
2. Check whether what broke is covered (COMPATIBILITY.md). Message text and page layout are
   not; names and behavior are.
3. If it is covered, it is a regression. Open an issue with both versions and the diff.
