---
title: "specification"
---

# Specification File

The `.rotini.spec.*` file is your CLI, as data. It is written in **YAML, JSON, JSONC or TOML** — the format is your choice; the schema is identical.

Two top-level keys: `version` (checked against the rotini binary running `generate`) and `command` (the root command — the binary itself). Everything else is a command key, because **rotini is commands all the way down**: the root is just the outermost one.

{{< alert type="info" title="EDITOR SUPPORT:" >}}
Point your editor at the schema and every key is completed and checked as you type. Either add a `$schema` key to the spec —

```yaml
$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/v1.0.0/schema-spec.json
```

— or, for an offline or forked setup, have `rotini generate` write the schema into your project with the conf's `generate.schemas` block and point at that copy instead, either with `$schema` or a `# yaml-language-server: $schema=<path>` comment on the first line.
{{< /alert >}}

{{< alert type="info" title="EVERY KEY:" >}}
This page is the tour. [**The spec reference**](reference/) is the complete list — every key, every constraint, rendered from the schema itself, so it cannot drift from what `rotini validate` accepts.
{{< /alert >}}

## A worked example

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0
command:
  name: acme
  description: the acme control cli
  usage: acme <command> [flags]
  footer: Use "acme help <command>" for more.
  examples:
    - acme deploy web --replicas 3

  env_prefix: ACME

  # --- input channels, all declared ---
  flags:
    - name: verbose
      summary: verbose output
      identifiers: [--verbose, -v]
      cascading: true            # available to every sub-command
      schema: {type: bool}

  env:
    - name: token
      summary: api token
      schema: {type: string, variable: ACME_TOKEN, secret: true}

  config:
    - name: endpoint
      schema: {type: string, default: https://api.acme.test}

  config_files:
    - name: project
      discover: {strategy: walk-up, file: .acme.yaml}
    - name: user
      discover: {strategy: xdg, file: config.yaml, app: acme}

  commands:
    - name: deploy
      summary: deploy a service
      group: core
      aliases: [dep]
      arguments:
        - name: service
          schema: {type: string, required: true, enum: [web, api]}
        - name: rest
          schema: {type: "[]string"}       # variadic
      flags:
        - name: replicas
          identifiers: [--replicas, -r]
          schema: {type: int, minimum: 1, maximum: 10, default: 1}
        - name: dry-run
          identifiers: [--dry-run]
          schema: {type: bool}
      flag_groups:
        - kind: mutually_exclusive
          flags: [replicas, dry-run]
      stdin:
        format: yaml
{{< /code >}}

## The channel inventory

Every way data reaches your CLI is declared. `rotini.Collect[T]` reconciles all of it in one call, in a documented precedence order.

| Channel | Spec surface |
|---|---|
| argv flags | `flags` — typed, clustering, `count`, repeatable, `cascading` |
| argv positionals | `arguments` — variadic, `passthrough` |
| value sentinels | `from: [file]` (`@path`), `from: [stdin]` (`-`) |
| environment | `env`, `env_prefix`, or a nested family via `nesting` |
| configuration files | `config_files` — a fixed `path`, `walk-up`, or `xdg` |
| **config path from a flag or env var** | `config_source` — the declarative two-phase parse |
| configuration values | `config` — read by dotted `key`, optionally pinned to one `file` |
| stdin | `stdin` — a typed, schema-validated payload |
| defaults | `schema.default` on any input |

## Validation

`rotini validate` is the gate, and it runs before any code is generated:

- the **JSON Schema** rejects what it can express — unknown keys, wrong types, bad patterns — and your editor shows it inline
- **33 lint rules** reject what a schema cannot: duplicate flag identifiers across a chain, a `config_source` naming a file that does not exist, a `$ref` cycle, a `count` flag carrying a default, an input whose `type` is not a Go type, a variadic argument that is not last

Problems are reported with a `file:line:col` in YAML, JSON and JSONC — and in TOML.

## Composition

A command can be pulled in from another spec instead of being written inline:

| Mode | How |
|---|---|
| Standalone | `name:` — the default; a generated stub |
| Inline + passthrough | `name:` plus `handler: {import, convention}` — own types, delegated handler code |
| Local `$ref` | `$ref: ../child/.rotini.spec.yaml` — same module; auto-delegates to the child's package |
| Module `$ref` | `$ref: mod://example.com/m@v1.2.3/cli/.rotini.spec.yaml` — read from the Go module cache, pinned by `go.sum` |
| Remote command | `remote_commands` / `remote_discovery` — dispatch to a sibling binary at run time, `git`-style |

`git::` and raw `https://` refs are **refused**: rotini has no fetcher, so codegen never reaches the network.

## The exhaustive reference

Every key, every shape, with commentary — and validated by the test suite, so it cannot drift from the schema:

**[`reference/.rotini.spec.yaml`](https://github.com/go-rotini/rotini/blob/main/reference/.rotini.spec.yaml)**
