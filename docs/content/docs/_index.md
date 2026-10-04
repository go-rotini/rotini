---
title: "docs"
---

# Using rotini

This page walks through a rotini CLI from install to test. The [quick start](/) is the short
version; the [spec](/specification) and [conf](/configuration) pages list every key.

## Install

rotini is one module with two parts: the **tool** that generates your code and the **runtime**
that code imports. Add both: the tool as a tool dependency, so every developer on the project
resolves the same version through `go.mod`, and the runtime as a regular one:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir todo
cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go get github.com/go-rotini/rotini@latest
{{< /code >}}

rotini requires Go 1.27 or later.

The `version:` key at the top of your spec and conf is the minimum rotini they need. An older
rotini, or a different major version, refuses to generate from them.

## The files and the loop

{{< code title="rotini init" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini init todo
{{< /code >}}

It prints the spec and conf it wrote, then the time and how long it took, the same report
`go generate` gives. Along with those two files it writes:

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

`validate` checks the spec against its JSON Schema and rotini's lint rules and reports each
problem with a `file:line:col`. `generate` runs the same checks first, so `validate` is mostly for CI.

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
  counts — are checked when the handler reads its inputs with `rtx.Inputs`, which the generated
  stub does first thing, and a bad value is a usage error naming the flag the user typed.
- **A flag works anywhere after the command that declares it**, including after a
  sub-command's name. A flag written *before* a sub-command's name belongs to a parent, which is
  what lets a parent and a sub-command both declare a flag with the same name.
- **`cascading: true` also shows a flag in its sub-commands' help.** It changes help only: every
  sub-command's page lists the flag under "Global Flags", where otherwise only its own command's
  page lists it. Parsing is the same either way, and a sub-command's handler sees the value
  either way, since its generated inputs include every parent's flags. So a flag that
  sub-commands are meant to use should be `cascading: true`, so their help pages show it.

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
yours. (It is removed if its command leaves the spec; delete its `var _ rotini.Handler` line or
list it under the conf's `keep:` to hold on to it.) Fill in `Run`:

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*todoAddHandler)(nil)

type todoAddHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*todoAddHandler) Run(ctx context.Context, rtx *rotini.Context) {
	// One line reconciles every declared channel: argv, env, config files, stdin
	// and defaults, in the documented precedence order.
	inputs, err := rtx.Inputs[TodoAddInputs]()
	if err != nil {
		rtx.HaltWith(err) // record it and stop; the reporter picks the exit code
		return
	}

	fmt.Fprintln(rtx.Stdout, "added:", inputs.TodoAdd.Arguments.Title)
	rtx.RecordSuccess("task added")
}
{{< /code >}}

- **`TodoAddInputs` is generated** from the spec, so the compiler holds the handler to it.
- **Five hooks, in this order:** `CascadingPreRun` runs for every command on the path from the
  root to the invoked command, root first. `PreRun`, `Run` and `PostRun` run only for the
  invoked command. `CascadingPostRun` runs for every command on the path again, invoked command
  first. Teardown (`PostRun` and `CascadingPostRun`) runs even after a halt or panic, for exactly
  the hooks whose setup ran. The `No*` embeds are no-ops; declare a method to use one.
- **Write to `rtx.Stdout`**, not `os.Stdout`, so tests and REPLs can capture it.
- **Record results and errors** (`rtx.RecordSuccess`, `rtx.RecordWarning`, `rtx.HaltWith`)
  rather than printing them — the runtime reports them once, after teardown.

The runtime only works out which command was invoked. Flags and arguments are parsed and
validated when a handler calls `rtx.Inputs[T]()` (or the per-channel `rtx.ArgvInputs`,
`EnvInputs`, `FileInputs` and `StdinInputs` methods). The generated handler stubs call it first
thing. A handler that never calls it gets the raw `rtx.Argv` and no validation, which is useful
when you want to bring your own parser and a trap if you delete the call by accident.

A dependency the handlers share — a database, an API client — is registered once in `main.go`
and read in any hook:

{{< code title="sharing a dependency" language="golang" open="true" collapsible="false" copy="true" >}}
var Store = rotini.NewDependency[*store.Store]("todo.store") // in the cmd package

cmd.Program.WithDependency(cmd.Store, openStore())          // in main.go
s := rtx.MustGetDependency(Store)                            // in a handler
{{< /code >}}

## Errors and exit codes

A handler that fails calls `rtx.HaltWith(err)`. By default the runtime prints each recorded
error to stderr as `Error: …` and exits 1. Every error carries a category —
`rotini.UsageError(err)` marks one as the user's to fix — so a program that wants distinct exit
codes installs its own reporting with `Program.WithReporter` and maps `rotini.CategoryOf(err)` to
a code. Declare `exit_status:` in the spec to document a command's codes in its man and
markdown pages.

## Help, completion and docs

The conf's `features:` turn on output generated from the spec:

| Feature | What you get |
|---|---|
| `help` (on in the conf `rotini init` writes) | `Help(path...)` pages, printed by `--help` and `help <command>` |
| `completion` | `Completion(shell)` scripts for bash, zsh, fish and PowerShell |
| `man` | `Man(path...)` man pages, in roff, plus a `ManSection` constant |
| `markdown` | `Markdown(path...)` reference pages |

A command exposes one with a few lines, e.g. a `completion` command whose handler prints the
script `Completion(shell)` returns.

Man pages are roff, the markup the `man` program reads, so `man -l taskr-add.1` displays one and
a package installs them like any other. Each page is named after its command path joined with
`-`: `taskr`, `taskr-add`. With `embed: true` the files are written under that name with the
section as the extension (`taskr-add.1`), so `cp renders/*.1 /usr/local/share/man/man1/` installs
them. The section is 1 unless the man feature sets `section:` (8 for a daemon or admin tool).
The header's date stays empty, so regenerating never changes a page, unless `SOURCE_DATE_EPOCH`
is set when you generate.

A rotini program can also be completed by another program, such as the host of a plugin, in
whatever format that host reads. rotini computes the answer: the candidates, their descriptions,
your completers' results and the spec's `complete:` hint. A `CompletionFormat` writes that
answer in the host's protocol. `rotini.CobraCompletion` is the built-in for Cobra-built hosts,
and any other protocol is a function with the same signature.

kubectl completes a plugin by running `kubectl_complete-<plugin>` and reading Cobra's format
back. `Program.Complete(words, format)` answers one request, so the plugin's binary can serve
as its own completer when installed under that second name:

{{< code title="cmd/kubectl-ctx/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
if strings.Contains(filepath.Base(os.Args[0]), "_complete-") {
	code, _ := cmd.Program.Complete(os.Args[1:], rotini.CobraCompletion)
	os.Exit(code)
}
{{< /code >}}

Docker and Flux instead complete a plugin by running the plugin's own hidden `__complete`
command. For those, set the format that command answers in, in `main.go`:
`cmd.Program.WithCompletion(rotini.CobraCompletion).Execute()`. Leave it unset for a standalone
CLI: rotini's own generated completion scripts expect rotini's format.

Set `display_name: kubectl ctx` on the root of such a plugin's spec, and its help, man and
markdown pages show `kubectl ctx …`, the command the user typed, rather than the binary name
`kubectl-ctx`.

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
