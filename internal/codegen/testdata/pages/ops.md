# ops

run the fleet

## Usage

```
ops [flags] <command>
```

A word starting with `@` names a response file (`@args`), whose lines are read as arguments, one per line, until `--`; to pass a word starting with `@`, double it (`@@x`).

## Commands

- `note` — add a note
- `exec` — run a command
- `run` — run a command on a host

## Data Commands

Commands that read a document from standard input.

- `import` — import tasks
- `upper` — upper-case lines (experimental)

## Deploying Commands

Commands that change what runs where.
Each one asks before it acts.

- `deploy` — ship a service (beta)

## Help Topics

- `filters` — how filters work
- `environment` — the environment variables ops reads

## Flags

- `-h, --help` — print help

## Output

Flags that shape what ops prints.

- `-v, --verbose` — say more; repeat to count: -vvv
- `--color` `string` — when to color (default `auto`) (one of auto, always, never)

## Environment

- `OPS_TOKEN` `string` — the API token; at least 20 characters

  Get one from the console.

  .Keep it secret; a path such as C:\ops\token works too.

- `OPS_LOG` `string` — the log level (one of debug, info, error)
  - `debug` — everything
  - `info`
  - `error` — failures only

## Configuration

- `region` (region) `string` — the default region (beta); must look like eu-1

  The region commands use when none is given.

- `profile` (profile) `string` — the output profile (one of short, long)
  - `short` — one line each
  - `long` — every field