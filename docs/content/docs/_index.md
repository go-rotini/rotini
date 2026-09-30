---
title: "docs"
---

# Using rotini

This page walks through a rotini CLI from install to test. The [quick start](/) is the short
version; the [spec](/specification) and [conf](/configuration) pages list every key.

## Install

rotini is one module with two parts: the **tool** that generates your code and the **runtime**
that code imports. Install the tool as a tool dependency, so every developer on the project
resolves the same version through `go.mod`:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir todo && cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

That also adds the runtime to `go.mod`. rotini requires Go 1.27 or later.

The `version:` key at the top of your spec and conf is the minimum rotini they need. An older
rotini, or a different major version, refuses to generate from them.

## The files and the loop

{{< code title="rotini init" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini init todo
{{< /code >}}

| File | What it is | Who edits it |
|---|---|---|
| `cmd/todo/.rotini.spec.yaml` | the [spec](/specification): commands, flags, arguments and every other input | you |
| `cmd/todo/.rotini.conf.yaml` | the [conf](/configuration): where code is written, which extras are on | you |
| `cmd/todo/main.go` | the entrypoint, with the `//go:generate` line | you (created once) |
| `internal/cmd/todo/zz_rotini.go` | the [generated](/generated) types and wiring | rotini, on every `go generate` |
| `internal/cmd/todo/todo*.go` | one handler file per command | you (created once) |

Specs and confs can also be JSON, JSONC or TOML: `rotini init todo --format toml`.

{{< code title="the loop" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml   # optional
go generate ./...
go build ./cmd/todo
{{< /code >}}

`validate` checks the spec against its JSON Schema and 43 lint rules and reports each problem
with a `file:line:col`. `generate` runs the same checks first, so `validate` is mostly for CI.

## Commands, flags and arguments

The root command is the binary; `commands:` nests sub-commands to any depth. Each command
declares its own `flags:` and `arguments:`, and each input has a `schema:` saying its type
and rules:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - name: add
    summary: add a task
    aliases: [a]
    arguments:
      - name: title
        summary: the task title
        schema: { type: string, required: true, minLength: 1 }
    flags:
      - name: priority
        summary: how urgent
        identifiers: [-p, --priority]
        schema: { type: string, default: normal, enum: [low, normal, high] }
      - name: tag
        summary: a label (repeatable)
        identifiers: [--tag]
        schema: { type: '[]string' }
{{< /code >}}

- **Types** are Go names (`string`, `int`, `bool`, `[]string`, `map[string]string`) or value
  types rotini parses for you: `duration`, `date`, `url`, `ip`, `bytesize` and
  [more](/specification#type).
- **Rules** — `required`, `default`, `enum`, `pattern`, `minimum`/`maximum`, lengths and item
  counts — are checked before your handler runs, and a bad value is a usage error naming the
  flag the user typed.
- **A flag works anywhere after the command that declares it**, including after a
  sub-command's name. A flag written *before* a sub-command's name belongs to a parent.

## Where values come from

A flag can also be read from an environment variable and a configuration file. Give it a
`key:` and declare the file:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  env_prefix: TODO
  config_files:
    - name: user
      discover: { strategy: xdg, app: todo, file: config.yaml }
  commands:
    - name: add
      flags:
        - name: priority
          identifiers: [-p, --priority]
          schema: { type: string, default: normal, key: defaults.priority }
{{< /code >}}

`--priority` now falls back to `TODO_DEFAULTS_PRIORITY`, then to `defaults.priority` in
`~/.config/todo/config.yaml`, then to its default. The command line always wins. Use
`variable:` to name the environment variable exactly instead of deriving it.

Commands can also declare pure `env:` and `config:` inputs, and a typed `stdin:` payload; they
all land in the same generated struct.

## Handlers

`go generate` creates one handler file per command, once, and never overwrites it — it is
yours. (It is removed if its command leaves the spec; delete its `var _ rotini.Handlers` line or
list it under the conf's `keep:` to hold on to it.) Fill in `Run`:

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handlers = (*todoAddHandlers)(nil)

type todoAddHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*todoAddHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	// One line reconciles every declared channel: argv, env, config files, stdin
	// and defaults, in the documented precedence order.
	inputs, err := rotini.Collect[TodoAddInputs](rtx)
	if err != nil {
		rtx.HaltWith(err) // record it and stop; the funnel picks the exit code
		return
	}

	fmt.Fprintln(rtx.Stdout, "added:", inputs.TodoAdd.Arguments.Title)
	rtx.RecordSuccess("task added")
}
{{< /code >}}

- **`TodoAddInputs` is generated** from the spec, so the compiler holds the handler to it.
- **Five hooks run for every command**: `CascadingPreRun` (also for each descendant), `PreRun`,
  `Run`, `PostRun`, `CascadingPostRun`. The `Default*` embeds are no-ops; declare a method to
  use one.
- **Write to `rtx.Stdout`**, not `os.Stdout`, so tests and REPLs can capture it.
- **Record results and errors** (`rtx.RecordSuccess`, `rtx.RecordWarning`, `rtx.HaltWith`)
  rather than printing them — the runtime reports them once, after teardown.

A service the handlers share — a database, an API client — is bound once in `main.go` and read
in any hook:

{{< code title="sharing a service" language="golang" open="true" collapsible="false" copy="true" >}}
var StoreKey = rotini.NewKey[Store]("todo.store")   // in the cmd package

cmd.StoreKey.Provide(cmd.Program, openStore())       // in main.go
store := StoreKey.MustGet(rtx)                       // in a handler
{{< /code >}}

## Errors and exit codes

A handler that fails calls `rtx.HaltWith(err)`. By default the runtime prints each recorded
error to stderr as `Error: …` and exits 1. Every error carries a category —
`rotini.UsageError(err)` marks one as the user's to fix — so a program that wants distinct exit
codes installs its own reporting with `Program.WithFunnel` and maps `rotini.CategoryOf(err)` to
a code. Declare `exit_status:` in the spec to document a command's codes in its man and
markdown pages.

## Help, completion and docs

The conf's `features:` turn on output generated from the spec:

| Feature | What you get |
|---|---|
| `help` (on in the conf `rotini init` writes) | `Help(path...)` pages, printed by `--help` and `help <command>` |
| `completion` | `Completion(shell)` scripts for bash, zsh, fish and PowerShell |
| `man` | `Man(path...)` man pages |
| `markdown` | `Markdown(path...)` reference pages |

A command exposes one with a few lines, e.g. a `completion` command whose handler prints the
script `Completion(shell)` returns.

## Testing

Build a fresh `Program` per test and run it with arguments — no process, no `os.Exit`:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAdd(t *testing.T) {
	var out bytes.Buffer
	code, err := NewProgram(Handlers()).WithStdout(&out).Run([]string{"add", "buy milk"})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if !strings.Contains(out.String(), "added: buy milk") {
		t.Errorf("stdout = %q", out.String())
	}
}
{{< /code >}}

## Composing CLIs

A command can be another CLI's spec: `- $ref: ../db/.rotini.spec.yaml` mounts it as a
sub-command, and it still builds and ships on its own. Keys set next to the `$ref` (a new
`name`, `summary`, `group` …) adjust it for its new parent.

## Versions

`main.go` passes `version` to `WithVersion`; stamp it at build time:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go build -ldflags "-X main.version=1.2.3" ./cmd/todo
./todo --version   # 1.2.3
{{< /code >}}
