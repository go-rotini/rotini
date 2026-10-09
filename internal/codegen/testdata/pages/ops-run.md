# ops run

run a command on a host

## Usage

```
ops run [flags] <host> [--] <command...>
```

## Arguments

- `<host>` — where to run it (one of edge, core)
  - `edge` — the edge pool
  - `core`
- `<command...>` — the command and its arguments (passed through as typed)

## Flags

- `-f, --format` `string` — how to print results (default `text`) (one of text, json, yaml)
  - `text` — aligned columns
  - `json`
  - `yaml` — human-friendly

## Global Flags

- `-h, --help` — print help
- `-v, --verbose` — say more; repeat to count: -vvv
- `--color` `string` — when to color (default `auto`) (one of auto, always, never)