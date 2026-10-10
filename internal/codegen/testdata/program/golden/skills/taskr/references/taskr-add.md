# taskr add

add a task

Effects: write

## Usage

```
taskr add [flags] <title>
```

## Arguments

- `<title>` — what to do; at least 1 character

## Flags

- `-t, --tag` `[]string` — tags for the task; repeatable; each value at most once
- `--priority` `string` — how urgent it is (default `low`) (one of low, high) (experimental)
- `--due` `date` — when it is due

## Global Flags

- `-v, --verbose` — say more; repeat to count: -vvv
- `-C, --dir` `string` — run as if started in this directory
- `-h, --help` — show help

## Examples

```
taskr add "buy milk" --tag home
```

## Output

Writes `Task` to stdout.

- `done` `bool`
- `id` `integer` (required)
- `title` `string` (required)

## Exit Status

- `3` (`duplicate`) — a task with this title exists
