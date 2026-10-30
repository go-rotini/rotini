# ops deploy

.ship is not reversible.

Read the runbook first.

ship a service

This command is in beta: it may change in a minor release.

## Usage

```
ops deploy [flags] <service> [COUNT]
```

## Arguments

- `<service>` — the service to ship; 2 to 63 characters; matches `^[a-z][a-z0-9-]*$`

  The service to ship, by its registry name.

  Names are lowercase.

- `[COUNT]` — how many copies; at least 1

## Flags

- `-p, --port` `int` — listen port (default `8080`); between 1 and 65535

  The port the service listens on.

- `--ratio` `float64` — traffic share (experimental); greater than 0 and at most 1
- `--step` `int` — rollout step; a multiple of 5
- `--tag` `[]string` — a label; 1 to 5 values; each value at most 10 characters; several values per occurrence, separated by ","; repeatable
- `--timeout` `duration` — how long to wait; at least 1s and less than 1h
- `-e, --env` `map[string]string` — extra environment; at most 8 values; repeatable
- `--token` `string` — the deploy token (@file or - for stdin)
- `--key` `string` — the signing key (@file)
- `--label` `[]string` — a label to apply (default `["a,b", "c\"d"]`); several values per occurrence, separated by ","; repeatable
- `--zone` `[]string` — the zones to use (default `["eu", "us"]`); repeatable
- `--quote` `string` — the quoting style (one of ``a`b``, `plain`); matches ``^[a-z`"]+$``

## Global Flags

- `-h, --help` — print help
- `-v, --verbose` — say more; repeat to count: -vvv
- `--color` `string` — when to color (default `auto`) (one of `auto`, `always`, `never`)

## Exit Status

- `0` — shipped
- `4` (`rejected`) — the cluster refused it ([docs](https://example.com/ops/rejected))

Report problems at https://example.com/ops/issues.