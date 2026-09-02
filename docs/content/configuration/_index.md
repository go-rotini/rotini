---
title: "configuration"
---

# Configuration File

The `.rotini.conf.*` file controls **codegen**, not your CLI's behavior. Where generated code is written, which derived outputs are produced, and how validation reports problems. Same four formats as the spec.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0

generate:
  # Opt-in: write rotini's own JSON Schemas into the project so an editor
  # `# yaml-language-server: $schema=<path>` comment resolves locally.
  schemas:
    spec:
      file: cmd/acme/.rotini-schema.spec.json
    conf:
      file: cmd/acme/.rotini-schema.conf.json

  packages:
    - type: main                              # the entrypoint — CREATE-ONCE
      file: cmd/acme/main.go
      package: main
    - type: cmd                               # generated file + editable stubs
      file: internal/cmd/acme/zz_acme.go
      package: acme

  features:
    - type: help
      enabled: true
    - type: completion
      enabled: true
    - type: man
      enabled: false
    - type: markdown
      enabled: false

validate:
  fail: collect                               # or `fast` — stop at the first problem
{{< /code >}}

## Packages

Two targets. Everything rotini writes goes to one of them.

| `type` | Contents | Overwritten? |
|---|---|---|
| `main` | the binary entrypoint, carrying the `//go:generate` directive | **never** — it holds your build metadata |
| `cmd` | the one generated file, plus one editable handler stub per command | the generated file every pass; stubs never |
| `models` | **optional** — the typed input/output structs, in their own package | every pass |

### When you need `models`

A command can source its handler from another package instead of a generated stub:

{{< code title="handler passthrough" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - name: deploy
    handler:
      import: deployh example.com/acme/handlers
      convention: Deploy
{{< /code >}}

That makes the **cmd package import the handler package**. If the handler then needs its own generated input type — and `rotini.Collect[T]` means it does — it would have to import `cmd` back. That is an import cycle, and Go rejects it.

Declaring a `models` target moves the typed structs into a package that imports nothing local, so `cmd` and the handler package can both import it:

{{< code title="models target" language="yaml" open="true" collapsible="false" copy="true" >}}
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
    - type: models
      file: internal/models/zz_models.go
      package: models
{{< /code >}}

{{< alert type="info" title="YOUR HANDLER CODE DOES NOT CHANGE:" >}}
The cmd package re-exports every model as a **type alias**, so a handler living in `cmd` still writes `rotini.Collect[AcmeDeployInputs](rtx)` whether the split is on or off. Only an outside handler package qualifies them — `models.AcmeDeployInputs`.
{{< /alert >}}

Omit the target — the default — and the structs stay in the cmd file. Pointing it at the cmd file itself is a legal no-op.

{{< alert type="info" title="NO RUNTIME TARGET:" >}}
The rotini runtime is an ordinary library dependency your generated code imports — `go get github.com/go-rotini/rotini` — not emitted code. Nothing here decides where it lands, because it does not land anywhere.
{{< /alert >}}

`keep` spares hand-written files in a managed directory from pruning. It is meant to stay empty.

## Features

Four derived outputs — `help`, `completion`, `man`, `markdown` — each **off by default**, each with two orthogonal sourcing knobs:

| Knob | `false` (default) | `true` |
|---|---|---|
| `embed` | content is an inline string literal; the generated `.go` is self-contained | content is rendered to a file under `embed_dir` and referenced with `//go:embed` |
| `template` | pages render from rotini's built-in template | an editable `*.tmpl` is seeded into `template_dir` and pages render from it |

`help`, `man` and `markdown` are **render-or-verbatim**: a command that sets that key in its spec has the string written exactly; otherwise the page is rendered from the command's structured doc fields.

`completion` is the exception — per-shell scripts generated from the program name, with no editable template. It has no `template`, and setting one is a warning.

## Validation reporting

Strictness is not configurable; validation is always strict. Only the reporting mode is:

- **`collect`** (default) — run to completion and report every problem at once
- **`fast`** — stop at the first

The `--fail` flag overrides the file.

## The exhaustive reference

Every key, with commentary — validated by the test suite:

**[`reference/.rotini.conf.yaml`](https://github.com/go-rotini/rotini/blob/main/reference/.rotini.conf.yaml)**
