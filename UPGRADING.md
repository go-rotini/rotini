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
`var _ rotini.Handler = (*xHandlers)(nil)` — and that marker is what makes it prunable. It
survives your edits, so an edited handler file is still pruned when its command goes. A helper
you put beside your handlers is never touched, whatever it is named. To keep a handler file
whose command is gone, delete its marker line or list it under `keep:`. Every prune is
reported:

```
Warning: pruned demo_ship.go; its command is no longer in the spec
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

## Adopting short-circuit flags (1.3)

A project made by `rotini init` before 1.3 answers `--help` in every handler, before it reads
its inputs, because reading them first would fail on a missing required value. From 1.3 a flag
can be marked `short_circuit: true`: when it is set on the command line, every declared
requirement is waived, so `rtx.Inputs` succeeds and the handler can act on the flag. Parse
errors (an unknown flag, a value that is not a number) are still reported.

Nothing changes until you opt in; a project left as it is regenerates exactly as before. To
move to the shape a fresh `rotini init` writes:

1. In the spec, mark the root's help flag `cascading: true` and `short_circuit: true`, and its
   version flag `short_circuit: true`. Raise `version:` to 1.3.0.
2. Add a `CascadingPreRun` to the root handler that answers both flags. The root handler is
   never rewritten, so this is yours to add; `rotini generate` warns until it is there. A fresh
   `rotini init` in a scratch directory shows the hook to copy:

   ```go
   func (*todoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
   	inputs, err := rtx.Inputs[TodoInputs]()
   	if err != nil {
   		rtx.HaltWith(err)
   		return
   	}

   	if inputs.Todo.Flags.Help {
   		var path []string
   		for _, c := range rtx.CommandChain()[1:] {
   			path = append(path, c.Name)
   		}

   		page, err := Help(path...)
   		if err != nil {
   			rtx.HaltWith(err)
   			return
   		}

   		fmt.Fprintln(rtx.Stdout, page)
   		rtx.HaltWithCode(0)
   		return
   	}

   	if inputs.Todo.Flags.Version {
   		fmt.Fprintln(rtx.Stdout, rtx.Version())
   		rtx.HaltWithCode(0)
   		return
   	}
   }
   ```

   Remove the `rotini.NoCascadingPreRun` line from the root handler's struct, since it now
   declares the hook itself.
3. Remove the per-command `help` flags the cascading one replaces, then run `rotini validate`.
4. Optionally delete the help check at the top of each existing handler. It no longer runs,
   because the root answers first, but it does no harm. New handler stubs are written without it.

A command that declares its own help flag keeps its own check, in both shapes.

## Flag sources in help, man and markdown (1.3)

A flag with an environment or config fallback (`key:` or `variable:`) now has a line under it
naming the variables it reads, in lookup order, and its config key when the command reads a
config file:

```
  -r, --replicas int    how many instances (default 1)
                        env: CFGCTL_DEPLOY_REPLICAS · config: deploy.replicas
```

Regenerating adds it to every rendered page. If you had named the variable in the flag's
summary so users could find it, you can drop that now.

A template you seeded with `template: true` is yours and is not changed. Its flag rows already
carry `.Env` and `.ConfigKey`; to show them, add this after the flag line in the Flags and
Global Flags sections of `help.txt.tmpl`:

```
{{if or .Env .ConfigKey}}  	{{with .Env}}env: {{join . ", "}}{{end}}{{if and .Env .ConfigKey}} · {{end}}{{with .ConfigKey}}config: {{.}}{{end}}
{{end}}
```

The built-in `man.txt.tmpl` and `markdown.md.tmpl` show the same; a fresh `rotini init` with
`template: true` in a scratch directory seeds copies to compare against. The contract document's
flags gain `env` and `config_key` in the same way.

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
- **`plugins` / `plugin_discovery`** — plugin binaries are dispatched at run time, not
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
