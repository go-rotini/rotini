---
name: taskr
description: "keep a list of tasks. Use when the user asks to add, list or clean up tasks."
allowed-tools: "Bash(taskr list *) Bash(taskr ls *) Bash(taskr remote show *)"
---

# taskr

Taskr keeps a list of tasks in a file and syncs it with a server.

## Commands

- `taskr add`: add a task (write)
- `taskr list`: list tasks (read, idempotent)
- `taskr purge`: delete finished tasks (destructive, idempotent)
- `taskr import`: add tasks read from stdin (write)
- `taskr remote sync`: sync with the server (destructive, open world)
- `taskr remote show`: show the server's address (read)
- `taskr remote login`: log in with a key file (write, open world)

## How to call it

- Write the command's words first, then its flags: `taskr <command> [flags] [arguments]`. Permission rules match the start of the command line, so a flag written before the command can make a harmless call need approval.
- Never wait for a prompt. A command that would ask for confirmation takes its confirm flag instead, listed below.
- For output you can parse, add the command's JSON flag, listed below. It writes JSON to stdout; messages and errors go to stderr.
- Before running a destructive command, run it with its dry-run flag to see what it would do.
- Effects say what a command does: read changes nothing, write creates or changes things, and destructive deletes or overwrites what can't be got back. A command with no effects listed may do any of these.

## Exit codes and errors

- 0: success.
- 1: an error: the command line or its inputs were wrong, a check failed, or the command failed. The message on stderr says which.
- 128 + n: stopped by signal n (130 for Ctrl-C).
- When the program writes structured errors, each is one JSON line on stderr, `{"schema_version":1,"error":{…}}`. Its `category` is `usage` (fix the command line and try again; `kind` says how: unknown-flag, unknown-command, needs-value, invalid-value, enum-violation, constraint-violation, missing-required, too-many-arguments, misplaced-flag), `internal` (a bug or misconfiguration; don't retry) or `none`. `candidates` lists the words a mistyped `token` was checked against.

## `taskr add`

add a task

- Effects: write
- Exit 3 (duplicate): a task with this title exists
- Reference: [references/taskr-add.md](references/taskr-add.md)

```
taskr add "buy milk" --tag home
```

## `taskr list`

list tasks

- Also: `ls`
- Effects: read, idempotent
- JSON output: add `--format=json`
- Reference: [references/taskr-list.md](references/taskr-list.md)

## `taskr purge`

delete finished tasks

- Effects: destructive, idempotent
- Preview: `--dry-run`
- Confirm without a prompt: `--yes`
- Exit 75 (busy): the list is locked (running it again may succeed)
- Reference: [references/taskr-purge.md](references/taskr-purge.md)

## `taskr import`

add tasks read from stdin

- Effects: write
- Reference: [references/taskr-import.md](references/taskr-import.md)

## `taskr remote sync`

sync with the server

- Effects: destructive, open world
- Reference: [references/taskr-remote-sync.md](references/taskr-remote-sync.md)

## `taskr remote show`

show the server's address

- Effects: read
- Reference: [references/taskr-remote-show.md](references/taskr-remote-show.md)

## `taskr remote login`

log in with a key file

- Effects: write, open world
- Reference: [references/taskr-remote-login.md](references/taskr-remote-login.md)
