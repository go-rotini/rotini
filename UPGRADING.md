# Upgrading rotini

Upgrading is: take the new version, regenerate, read the diff, build. This document says what
to expect at each step, and what rotini will and will not touch in your repository.

See [COMPATIBILITY.md](COMPATIBILITY.md) for what a version number promises.

## The short version

```bash
go get -u github.com/go-rotini/rotini          # the runtime
go get -tool github.com/go-rotini/rotini@latest # the tool — same module, same version
go generate ./...                               # regenerate
git diff                                        # read it
go build ./... && go test ./...
```

Taking a patch or minor release does **not** require editing your spec or conf: the `version:`
key in each is a minimum, not a pin (see COMPATIBILITY.md). If validation complains that the
binary is older than your document, you upgraded the runtime and not the tool, or the other
way round — they are one module and must be at one version.

## What regenerating touches

This is the part worth knowing precisely, because it is the only operation that can destroy
work.

| file | on regenerate |
|---|---|
| the generated file (`zz_rotini.go` by default, wherever `packages[type=cmd].file` points) | **rewritten every time.** Never edit it; the header says so. |
| the models file, when `packages[type=models]` is declared | **rewritten every time.** |
| a handler stub (`<root>_<path>.go`) | **created once, then never touched.** Your edits are safe. |
| `main.go` (`packages[type=main].file`) | **created once, then never touched.** It holds your build metadata and your service bindings. |
| an editable template under `template_dir` | **seeded once when missing, then never touched.** |
| rendered output under `embed_dir` (`help_*.txt`, `man_*.txt`, `markdown_*.md`, `completion_*.txt`) | rewritten, and pruned per feature. |
| the schema files named by `generate.schemas` | overwritten from the embedded copies, never pruned. |
| anything else in the cmd package | left alone unless it is an orphaned stub — see below. |

### Pruning, and the one way to lose work

The generator prunes handler stubs in the cmd package that no longer correspond to a command
in the spec. That is how renaming or deleting a command cleans up after itself, and it is the
one path that can delete code you wrote:

- **Renaming a command** (or changing its `filename:`) orphans the old stub. Regenerating
  deletes it and seeds a new, empty one. **Move your handler body first**, or recover it from
  git afterwards.
- **Deleting a command** deletes its stub, which is usually what you wanted.

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
- **no changes to your stubs or `main.go`.** If you see one, something is wrong — file it.

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
