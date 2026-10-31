# ops

run the fleet

## Usage

```
ops [flags] <command>
```

A word starting with `@` names a response file (`@args`), whose lines are read as arguments, one per line, until `--`; to pass a word starting with `@`, double it (`@@x`).

## Commands

- [`note`](ops-note.md) — add a note
- [`exec`](ops-exec.md) — run a command
- [`legacy`](ops-legacy.md) — ship the old way *(deprecated since 1.4.0, removed in 2.0.0: renamed; use ops deploy instead)*
- [`run`](ops-run.md) — run a command on a host

## Data Commands

Commands that read a document from standard input.

- [`import`](ops-import.md) — import tasks
- [`upper`](ops-upper.md) — upper-case lines (experimental)

## Deploying Commands

Commands that change what runs where.
Each one asks before it acts.

- [`deploy`](ops-deploy.md) — ship a service (beta)

## Help Topics

- [`filters`](ops-filters.md) — how filters work
- [`environment`](ops-environment.md) — the environment variables ops reads
- [`configuration`](ops-configuration.md) — the configuration files and keys ops reads

## Flags

- `-h, --help` — print help

## Output

Flags that shape what ops prints.

- `-v, --verbose` — say more; repeat to count: -vvv
- `--color` `string` — when to color (default `auto`) (one of `auto`, `always`, `never`)

## Environment

- `OPS_TOKEN` `string` — the API token; at least 20 characters

  Get one from the console.

  .Keep it secret; a path such as C:\ops\token works too.

- `OPS_LOG` `string` — the log level (one of `debug`, `info`, `error`)
  - `debug` — everything
  - `info`
  - `error` — failures only

## Configuration

- `region` `string` — the default region (beta); must look like eu-1

  The region commands use when none is given.

- `profile` `string` — the output profile (one of `short`, `long`)
  - `short` — one line each
  - `long` — every field

## Files

The first file listed that sets a key supplies it.

- `~/.ops.yaml`
- `.ops.yaml` — in the working directory or the nearest parent holding one

## Exit Status

- `0` — done
- `3` (`unreachable`) — the fleet did not answer (retryable)