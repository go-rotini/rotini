# ops exec

run a command

## Usage

```
ops exec <argv...>
```

Every word after `exec` is passed through as written, so flags go before `exec`.

## Arguments

- `<argv...>` — the command and its arguments

## Global Flags

- `-h, --help` — print help
- `-v, --verbose` — say more; repeat to count: -vvv
- `--color` `string` — when to color (default `auto`) (one of auto, always, never)