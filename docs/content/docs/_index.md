---
title: "docs"
---

# Using rotini

This guide builds `todo`, a small task CLI, from install to tests, and covers what you need to
write your own rotini program. The [quick start](/) is the short version; the
[spec](/specification) and [conf](/configuration) references list every key.

## Install

Rotini is one module with two parts: the **tool** that generates your code and the **runtime**
package that code imports. Add the tool as a tool dependency, so everyone working on the project
runs the same version through `go.mod`:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir todo
cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

Rotini requires Go 1.27 or later. The tool and the runtime are one module, so this also adds the
runtime to `go.mod`, as an indirect dependency until your code imports it.

## The files and the loop

`rotini init` sets up a program that builds and runs as it is. Run `go mod tidy` after it to
record the runtime as a direct dependency, since the generated code imports it:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini init todo
go mod tidy
{{< /code >}}

`init` prints the spec and conf it wrote, then the time and how long it took, the same report
`go generate` gives. These are the files it writes:

| File | What it is | Who edits it |
|---|---|---|
| `cmd/todo/.rotini.spec.yaml` | the [spec](/specification): commands, flags, arguments and every other input | you |
| `cmd/todo/.rotini.conf.yaml` | the [conf](/configuration): where code is written, which extras are on | you |
| `cmd/todo/.rotini-schema.*.json` | JSON Schemas for the spec and conf, for editor completion | rotini, on every `go generate` |
| `cmd/todo/main.go` | the entrypoint, with the `//go:generate` line | you (created once) |
| `internal/cmd/todo/zz_rotini.go` | the [generated](/generated) types and wiring | rotini, on every `go generate` |
| `internal/cmd/todo/todo*.go` | one handler file per command | you (created once) |

Specs and confs can also be JSON, JSONC or TOML: `rotini init todo --format toml`.

The spec `init` writes declares a `--help` flag, a `--version` flag, and `help` and `version`
commands. Rotini adds no flags or commands of its own, so these are ordinary spec entries you can
rename, change or delete.

From then on, development is a loop: change the spec, regenerate, implement any new handler, build.

{{< code title="the loop" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml   # optional
go generate ./...
go build ./cmd/todo
{{< /code >}}

`validate` checks the spec and conf against their JSON Schemas and rotini's lint rules, and
reports each problem with its `file:line:col`:

{{< code title="a mistyped key" language="text" open="true" collapsible="false" copy="false" >}}
$ go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml
spec: ./cmd/todo/.rotini.spec.yaml
conf: ./cmd/todo/.rotini.conf.yaml
Error: spec: ./cmd/todo/.rotini.spec.yaml:31:19: /command/commands/0/flags/0/sumary: unknown key "sumary" on a flag input
{{< /code >}}

`generate` runs the same checks first, so `validate` is mostly for CI.

## Commands, flags and arguments

The root command is the binary, and `commands:` nests sub-commands to any depth. Each command
declares its own `flags:` and `arguments:`, and each input has a `schema:` that sets its type and
rules:

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
  types that rotini parses for you: `duration`, `date`, `url`, `ip`, `bytesize` and
  [more](/specification#type).
- **Rules** such as `required`, `default`, `enum`, `pattern`, `minimum` and `maximum`, lengths and
  item counts are checked when the handler reads its inputs with `rtx.Inputs`, which the generated
  handler does first. A bad value is a usage error naming the flag the user typed.
- **A flag works anywhere after the command that declares it**, including after a sub-command's
  name. A flag written *before* a sub-command's name belongs to a parent, which is what lets a
  parent and a sub-command both declare a flag with the same name.
- **`cascading: true` also shows a flag in its sub-commands' help.** It changes help only: every
  sub-command's page lists the flag under "Global Flags", where otherwise only its own command's
  page lists it. Parsing is the same either way, and a sub-command's handler sees the value either
  way, since its generated inputs include every parent's flags. Make a flag that sub-commands use
  `cascading: true`, so their help pages show it.

Mistakes are reported before your code runs:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo add "buy milk" -p urgent
Error: invalid value "urgent" for -p (one of: low, normal, high)
$ ./todo add
Error: missing required input: <title>
$ ./todo add "buy milk" --loud
Error: unknown flag "--loud"
{{< /code >}}

## Where values come from

A flag can also be read from an environment variable and a configuration file. Give it a `key:`
and declare the file:

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
`$XDG_CONFIG_HOME/todo/config.yaml` (by default `~/.config/todo/config.yaml`), then to its
default. The command line always wins:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ cat ~/.config/todo/config.yaml
defaults:
  priority: high
$ ./todo add "buy milk"
added "buy milk" (priority high)
$ TODO_DEFAULTS_PRIORITY=low ./todo add "buy milk"
added "buy milk" (priority low)
$ TODO_DEFAULTS_PRIORITY=low ./todo add "buy milk" -p normal
added "buy milk" (priority normal)
{{< /code >}}

Use `variable:` to name the environment variable exactly instead of deriving it. Config files can
also be found by walking up from the working directory (`strategy: walk-up`) or read from a fixed
`path:`; see [`config_files`](/specification#config_files).

Commands can also declare pure `env:` and `config:` inputs, and a typed `stdin:` payload. They all
land in the same generated inputs struct.

## Handlers

`go generate` creates one handler file per command, once, and never overwrites it: it is yours.
If its command leaves the spec, the file is removed and the removal reported. To keep it, delete
its `var _ rotini.Handler` line or list it under the conf's [`keep:`](/configuration#keep). Fill in
`Run`:

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
	// One call reads every declared source (command line, environment, config files, stdin
	// and defaults) in precedence order, and validates the result.
	inputs, err := rtx.Inputs[TodoAddInputs]()
	if err != nil {
		rtx.HaltWith(err) // record the error and stop; the reporter prints it and sets the exit code
		return
	}

	add := inputs.TodoAdd
	fmt.Fprintf(rtx.Stdout, "added %q (priority %s)\n", add.Arguments.Title, add.Flags.Priority)
}
{{< /code >}}

- **`TodoAddInputs` is generated** from the spec, so the compiler holds the handler to it.
- **Five hooks, in this order:** `CascadingPreRun` runs for every command on the path from the
  root to the invoked command, root first. `PreRun`, `Run` and `PostRun` run only for the invoked
  command. `CascadingPostRun` runs for every command on the path again, invoked command first.
  Teardown (`PostRun` and `CascadingPostRun`) runs even after a halt or panic, for exactly the
  hooks whose setup ran. The `No*` embeds are no-ops; declare the method to use a hook.
- **Write to `rtx.Stdout`**, not `os.Stdout`, so tests can capture it.
- **Record results and errors** with `rtx.RecordSuccess`, `rtx.RecordWarning` and `rtx.HaltWith`
  rather than printing them. The runtime reports them once, after teardown.

The runtime itself only works out which command was invoked. Flags and arguments are parsed and
validated when a handler calls `rtx.Inputs[T]()`, or one of the per-source methods
`rtx.ArgvInputs`, `EnvInputs`, `FileInputs`, `StdinInputs` and `DefaultInputs`. The generated
handlers call it first. A handler that never calls it gets the raw `rtx.Argv` and no validation,
so you can bring your own parser or collect inputs another way, and check what you collect with
[`rtx.CheckInputs`](#checking-inputs-you-collected-yourself).

A dependency the handlers share, such as a database or an API client, is registered once in
`main.go` and read in any hook:

{{< code title="sharing a dependency" language="golang" open="true" collapsible="false" copy="true" >}}
var Store = rotini.NewDependency[*store.Store]("todo.store") // in the cmd package

cmd.Program.WithDependency(cmd.Store, openStore())          // in main.go
s := rtx.MustGetDependency(Store)                            // in a handler
{{< /code >}}

### Flags that skip the run

Some flags replace a command's run instead of changing it: `--help`, `--version`, or a flag of
your own such as `--print-plan`. Mark one `short_circuit: true` in the spec. When it is set on
the command line, Rotini waives every requirement the spec declares for the command chain
(required inputs, enums, bounds, patterns, flag groups and flag dependencies), so `rtx.Inputs`
succeeds and your handler can act on the flag. Input that can't be read is still an error: an
unknown flag or command, a value of the wrong type, or too many arguments. Rotini takes no action
of its own; your handler checks the flag and decides what to do.

`rotini init` marks `--help` and `--version` this way, makes `--help` cascading so every command's
page lists it, and writes one `CascadingPreRun` in the root handler that answers both for every
command. So a command's handler needs no help code, and `todo add --help` works even with the
title missing.

A short-circuit flag can belong to one command, too:

{{< code title="a command-local short circuit" language="golang" open="true" collapsible="false" copy="true" >}}
// spec, under the deploy command:
//   - name: print-plan
//     summary: print what would be deployed, and deploy nothing
//     short_circuit: true
//     schema: { type: bool }

func (*deployHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[DeployInputs]() // succeeds even with <service> missing
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if in.Deploy.Flags.PrintPlan {
		fmt.Fprintf(rtx.Stdout, "%+v\n", in.Deploy)
		return
	}
	// … deploy
}
{{< /code >}}

A short-circuit flag must be a `bool`, can't be `required`, can't default to `true`, and can't be
read from an environment variable or config key; `rotini validate` reports any of these.
Completion offers nothing more once one is on the line.

### Checking inputs you collected yourself

When values come from somewhere rotini didn't read, such as a prompt, a secrets service or a
test, check them against the spec with `rtx.CheckInputs`. It applies the same rules `rtx.Inputs`
applies (required inputs, enums, bounds, patterns, flag groups and dependencies) and returns the
same errors:

{{< code title="checking prompted answers" language="golang" open="true" collapsible="false" copy="true" >}}
argv, err := rtx.ArgvInputs[TodoAddInputs]()
if err != nil {
	rtx.HaltWith(err)
	return
}

in := askForMissing(rtx, argv.Values) // your own prompting code

if err := rtx.CheckInputs(in, rotini.PresenceOf(in)); err != nil {
	rtx.HaltWith(err)
	return
}
{{< /code >}}

The second argument says which fields were supplied. `rotini.PresenceOf(in)` treats every
non-zero field as supplied; when a zero value (`false`, `0`, `""`) must count as supplied, build
the `rotini.Presence` yourself. A value's rules are checked only when it was supplied, with one
exception that matches the command line: a list flag or variadic argument left out has zero
items, so a `minItems` still applies to it unless it has a default. `CheckInputs` reads nothing,
applies no defaults and never changes the value, and it names each input by its usual spelling
(`--priority`, `<title>`).

A layer you build yourself can also join rotini's own in a merge. Its values are checked too when
you validate the merged report:

{{< code title="mixing your own source with rotini's" language="golang" open="true" collapsible="false" copy="true" >}}
argv, _ := rtx.ArgvInputs[TodoAddInputs]()
env, _ := rtx.EnvInputs[TodoAddInputs]()
vault := rotini.InputLayer[TodoAddInputs]{Name: "vault", Values: fromVault(), Set: vaultSet}

merged, report := rotini.MergeInputsWithReport(env, vault, argv)
if err := report.Validate(); err != nil {
	rtx.HaltWith(err)
	return
}
{{< /code >}}

## Errors and exit codes

A handler that fails calls `rtx.HaltWith(err)`. The program's reporter runs once, after
teardown, and decides what to print and which exit code to use. The default reporter prints each
warning to stderr as `Warning: …` and each error as `Error: …`, and exits 1 when anything failed,
unless a handler already set a non-zero code with `rtx.HaltWithCode`.

Every error carries a category, which `rotini.CategoryOf(err)` returns: `rotini.CategoryUsage`
for a mistake the user can fix, `rotini.CategoryInternal` for a fault in the program, and
`rotini.CategoryNone` for an error nobody classified. Parse and validation failures are already
usage errors. Mark your own with `rotini.UsageError(err)` or `rotini.InternalError(err)`.

### Handling errors in a handler

An error from `rtx.Inputs`, the per-source methods, `rtx.CheckInputs` or `report.Validate` is one
of two types:

- **`*rotini.ParseError`**: the command line, and validation of any value. Its `Kind` says what
  went wrong, `Token` holds the value at fault and `Candidates` what it could have been.
- **`*rotini.InputError`**: reading an environment variable, a config file or stdin. `Channel`
  and `Input` say which.

Branch on the type, the kind or the category, never on the message text, which can improve
between releases:

{{< code title="branching on an error" language="golang" open="true" collapsible="false" copy="true" >}}
inputs, err := rtx.Inputs[TodoAddInputs]()
if err != nil {
	var pe *rotini.ParseError
	switch {
	case errors.As(err, &pe) && pe.Kind == rotini.ParseKindMissingRequired:
		fmt.Fprintf(rtx.Stderr, "%s\nRun '%s --help' for usage.\n", err, rtx.CommandPath())
		rtx.HaltWithCode(2)
	case rotini.CategoryOf(err) == rotini.CategoryUsage:
		rtx.RecordError(err)
		rtx.HaltWithCode(2)
	default:
		rtx.HaltWith(err)
	}
	return
}
{{< /code >}}

The kinds are `ParseKindUnknownFlag`, `ParseKindUnknownCommand`, `ParseKindNeedsValue`,
`ParseKindInvalidValue`, `ParseKindEnumViolation`, `ParseKindConstraintViolation`,
`ParseKindMissingRequired`, `ParseKindNoArguments`, `ParseKindTooManyArguments` and
`ParseKindInternal`.

Each response is one call:

- **Point at help**: print a hint built from `rtx.CommandPath()`, or the whole page with
  `rtx.Help()`.
- **Suggest a correction**: `rotini.NewSuggestor().For(err)` ranks the `Token` against the
  `Candidates` and returns the nearest. Rotini never prints a suggestion itself.
- **Choose the exit code**: `rtx.HaltWithCode(n)`. The first non-zero code wins, and the default
  reporter keeps it. `rtx.HaltWithCode` records no error, so print the error yourself (as the
  first branch does) or record it with `rtx.RecordError` for the reporter to print.
- **Or just stop**: `rtx.HaltWith(err)` records the error and leaves the code to the reporter.

Three patterns cover most handlers. To fail with an error, use `rtx.HaltWith(err)`. To record an
error and carry on, collecting several, use `rtx.RecordError(err)` and decide later with
`rtx.Failed()`. When an exit code means something, use `rtx.HaltWithCode(n)` and list `n` under
the command's `exit_status:`.

Handle an error in the handler when the response depends on the command. For one policy across
the whole program, handle it in a reporter.

To give each category its own exit code, write a reporter. A reporter replaces the default
entirely: it prints only what it prints, and the exit code is only what it sets with `rtx.Exit`,
so give it a fallback code for errors with no category, or a failed run exits 0:

{{< code title="internal/cmd/todo/report.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

// Report prints warnings and errors to stderr, points at help after a usage error, and sets the
// exit code from the first error's category: 2 for a usage error, 70 for an internal one, 1 for
// anything else.
func Report(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	for _, w := range out.Warnings {
		fmt.Fprintln(rtx.Stderr, "Warning:", w)
	}
	for _, err := range out.Errors {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
	}
	for _, p := range out.Panics {
		fmt.Fprintln(rtx.Stderr, "Error:", p)
	}
	if len(out.Errors) > 0 && rotini.CategoryOf(out.Errors[0]) == rotini.CategoryUsage {
		fmt.Fprintf(rtx.Stderr, "Run '%s --help' for usage.\n", rtx.CommandPath())
	}
	if !out.Failed() {
		return
	}
	code := 1
	if len(out.Errors) > 0 {
		switch rotini.CategoryOf(out.Errors[0]) {
		case rotini.CategoryUsage:
			code = 2
		case rotini.CategoryInternal:
			code = 70
		}
	}
	rtx.Exit(code)
}
{{< /code >}}

Install it in `main.go` with `cmd.Program.WithReporter(cmd.Report)`. Keeping it in the command
package, rather than in `main.go`, lets tests install the same reporter (see [Testing](#testing)).
The `Outcome` also carries `Infos` and `Successes`, from `rtx.RecordInfo` and
`rtx.RecordSuccess`, for a reporter that prints those too.

For errors that scripts parse, `rotini.StructuredReporter` writes them as JSON lines; see
[errors scripts can read](#errors-scripts-can-read). Declare `exit_status:` in the spec to
document a command's codes in its man and markdown pages.

## Structured output

A command's inputs are already a contract: the spec says what it accepts, and rotini parses,
checks and documents it. `output:` does the same for the **shape** of what a command writes.
Rotini generates a Go type for it, documents it, and publishes it as JSON Schema for scripts and
other tools.

How the command writes that output, and in which format, stays the handler's own code. Rotini
adds no format flag and wires none. There are a few optional helpers for writing and checking
output, and a handler is free to ignore them.

Everything here is opt-in. A command without `output:` gets none of it.

### Declare the shape

`output:` is a schema. Named shapes go under the root's `schemas:`, and each becomes an exported
Go type. The format is an ordinary flag, declared here on the root with `cascading: true` so every
command's help lists it:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  schemas:
    Task:
      type: object
      required: [id, title, status]
      properties:
        id: { type: integer }
        title: { type: string }
        status: { type: string, enum: [open, done] }
    TaskList:
      type: object
      description: Every task, oldest first.
      properties:
        tasks: { type: array, items: { $ref: "#/schemas/Task" } }
  flags:
    # … the help and version flags `rotini init` declared
    - name: output
      summary: how to write the result
      identifiers: [-o, --output]
      cascading: true
      schema: { type: string, default: table, enum: [table, json, yaml, toml] }
  commands:
    - name: list
      summary: list tasks
      output: { $ref: "#/schemas/TaskList" }
{{< /code >}}

`rotini generate` writes the types `Task` and `TaskList`, and `TodoListOutput`, the `list`
command's output type. A command that writes a stream of items, one at a time, declares the
shape of **one item**.

An exit status can declare a shape too, for an outcome that still writes data:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  exit_status:
    - code: 3
      summary: some tasks failed; stdout lists the ones that succeeded
      output: { type: array, items: { $ref: "#/schemas/Task" } }
{{< /code >}}

### Write it

Choosing a format is up to you; here it is the value of the `--output` flag above.
`rtx.WriteOutput` is an optional helper that writes a value to stdout in the format you pass. It
writes json (indented), yaml and toml itself, and hands any other format to your renderer. Pass
`nil` as the renderer if you only ever use those three:

{{< code title="internal/cmd/todo/todo_list.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*todoListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TodoListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := rtx.WriteOutput(TodoListOutput{Tasks: load()}, in.Todo.Flags.Output, renderTable); err != nil {
		rtx.HaltWith(err)
	}
}

func renderTable(w io.Writer, format string, v TodoListOutput) error {
	fmt.Fprintln(w, "ID  STATUS  TITLE")
	for _, t := range v.Tasks {
		fmt.Fprintf(w, "%-3d %-7s %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}
{{< /code >}}

`load()` stands for wherever your tasks come from. The same value comes out in each format:

```console
$ todo list
ID  STATUS  TITLE
1   open    write docs
2   done    ship
$ todo list -o json
{
  "tasks": [
    {
      "id": 1,
      "status": "open",
      "title": "write docs"
    },
    {
      "id": 2,
      "status": "done",
      "title": "ship"
    }
  ]
}
```

An empty format means json. `rtx.WriteOutputItem` writes one item of a stream as it is ready:
one compact JSON value per line, or one YAML document per item. A stream can't be written as
toml.

Both return an internal error, and write nothing, when the value is not the command's
`<Prefix>Output` type or when a format they don't write has no renderer. Both are bugs in the
program, not mistakes by the user. A renderer's own error is returned as it is.

**Keep stdout for the output.** A script reading `todo list -o json` breaks if anything else
lands on stdout, such as a progress line before the JSON document. Write progress, notes and
prompts to `rtx.Stderr`.

### Check it

`Program.WithOutputChecks()` makes every `WriteOutput` and `WriteOutputItem` call check its value
against the declared shape before writing. It is off by default. Turn it on in tests, or in a
debug build:

{{< code title="todo_test.go" language="go" open="true" collapsible="false" copy="true" >}}
p := NewProgram(Handlers()).WithOutputChecks()
{{< /code >}}

With checks on, a value that does not match is an internal error naming each field at fault,
and nothing is written to stdout:

```console
$ todo list -o json
Error: todo list: output does not match its contract: output.tasks[2].status: value is not in enum
```

`rtx.CheckOutput(v)` makes the same check without writing anything.

`rotini.DecodeOutput[T]` reads captured stdout back in a test. It decodes json, yaml or toml
into the command's output type, checking it against the shape first. To read a stream written
with `WriteOutputItem`, use a slice of the type:

{{< code title="todo_test.go" language="go" open="true" collapsible="false" copy="true" >}}
var stdout bytes.Buffer
p := NewProgram(Handlers()).WithStdout(&stdout)
p.Run([]string{"list", "-o", "yaml"})
list, err := rotini.DecodeOutput[TodoListOutput](p, stdout.Bytes(), "yaml")
{{< /code >}}

### Errors scripts can read

`rotini.StructuredReporter` builds a reporter for programs whose output scripts read. You pass
it your own rule for when a run is structured. When the rule says yes, it writes each error,
warning, info and success to **stderr** as one JSON object per line, so stdout carries only the
output. When the rule says no, it reports exactly as the default reporter does.

The reporter runs after the command, and the run may have failed because parsing did, so the
rule reads the command line itself rather than validated inputs. For a program that declares a
`--json` flag:

{{< code title="cmd/todo/main.go" language="go" open="true" collapsible="false" copy="true" >}}
cmd.Program.WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
	return slices.Contains(rtx.Argv, "--json")
})).Execute()
{{< /code >}}

```console
$ todo list --json --bogus
{"error":{"category":"usage","command":"todo list","exit_code":1,"flag":"--bogus","kind":"unknown-flag","message":"unknown flag \"--bogus\"","token":"--bogus"}}
```

Exit codes are decided exactly as the default reporter decides them. Each line's shape is
described by
[schema-error.json](https://github.com/go-rotini/rotini/blob/main/schema-error.json).

### What gets generated

- **An OUTPUT section** in help, man and markdown pages: the shape's description, its type, and
  its top-level fields with their types and descriptions. An exit status that writes output
  says so. Its help heading is `headings.output`.
- **One JSON Schema per output**, with `generate.schemas.output.dir` in the conf:
  `todo-list.output.json` for a command, and `todo.exit-3.output.json` for an exit status.
  Each is standard JSON Schema (draft-07), with the named schemas it uses included.
- **The contract document**, with `generate.contract.file`: one JSON file describing every
  visible command, including its arguments, flags, environment variables, configuration keys,
  stdin, output shape and exit statuses. Each command also has a `parameters` JSON Schema
  covering its arguments and flags, so it maps directly onto a tool definition for an AI
  agent. Its format is described by
  [schema-contract.json](https://github.com/go-rotini/rotini/blob/main/schema-contract.json).

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  schemas:
    output:
      dir: schemas/output
  contract:
    file: cli-contract.json
{{< /code >}}

## Help, completion and docs

The conf's `features:` turn on output generated from the spec. Each adds functions to the
generated package:

| Feature | What you get |
|---|---|
| `help` (on in the conf `rotini init` writes) | `Help(path...)` pages, printed by `--help` and `help <command>` |
| `completion` | `Completion(shell)` scripts for bash, zsh, fish and PowerShell |
| `man` | `Man(path...)` man pages in roff, `ManPages()` for all of them, and a `ManSection` constant |
| `markdown` | `Markdown(path...)` reference pages and `MarkdownPages()` for all of them |

To expose one, add a command for it to the spec and call the function from its handler; for
example, a `completion` command whose handler prints the script `Completion(shell)` returns.

Man pages are roff, the markup the `man` program reads, so `man -l todo-add.1` displays one and a
package installs them like any other. Each page is named after its command path joined with `-`:
`todo`, `todo-add`. With `embed: true` the pages are also written as files under that name with the
section as the extension (`todo-add.1`), so `cp renders/*.1 /usr/local/share/man/man1/` installs
them. The section is 1 unless the man feature sets `section:` (8 for a daemon or admin tool). The
header's date stays empty, so regenerating never changes a page, unless `SOURCE_DATE_EPOCH` is set
when you generate.

A rotini program can also answer completion requests from another program, such as the host of a
plugin, in whatever format that host reads. A `rotini.CompletionFormat` writes the answer in the
host's protocol; `rotini.PluginCompletion` is the built-in one for kubectl, Docker and Flux, and
[tab completion](#tab-completion) shows how to use it.

## Plugins

A plugin is a separate program that runs as a sub-command of another one: `git lfs` runs a
binary named `git-lfs`. A rotini CLI can work with plugins in two directions: it can run other
programs as its own sub-commands, and it can itself be a plugin for kubectl, Docker or Flux.

### Run other programs as sub-commands

Declare the plugins your CLI knows about under `plugins:`, or turn on `plugin_discovery:` to
pick up any executable whose name starts with a prefix. Both can be used together:

{{< code title="cmd/plug/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: plug
  plugin_path: ./plugins/bin
  plugins:
    - name: sync
      summary: synchronize with the remote store
      aliases: [sy]
    - name: report
      summary: render a report
      timeout: 5s
  plugin_discovery:
    prefix: plug-
{{< /code >}}

- **A declared plugin** runs the binary `<program>-<name>`: `plug sync` runs `plug-sync`. It is
  listed in help and completion whether or not it is installed, so a missing binary is reported
  as an internal error: an install problem.
- **Discovery** makes any executable named `<prefix><word>` a sub-command: `plug hello` runs
  `plug-hello` if one exists. The prefix defaults to the program's name followed by `-`. A word
  that matches nothing is a usage error, since it is most likely a typo. A discovered name that
  collides with a declared command or plugin is skipped. Completion offers discovered plugins
  unless `hidden: true` is set, but the generated help can't list them, since they aren't known
  when you generate.
- **Both are searched for in the same order:** next to the program's own binary, then in
  `plugin_path` (relative to the working directory), then on `PATH`. A binary that isn't found
  is reported with the places searched:

  ```console
  $ plug absent
  Error: "plug-absent" not found; searched next to the binary /usr/local/bin, then the plugin path ./plugins/bin, then PATH
  ```

- **The plugin runs with** every argument after its name, and with the program's stdin, stdout
  and stderr. Its exit code passes through unchanged. A plugin that runs past its `timeout:` is
  killed, and so is one still running when the run is canceled, for example by Ctrl+C.

Rotini prints nothing about discovered plugins itself. To list them, in your own help or a
`plugins` command, call `DiscoveredPlugins()` on the command that has discovery:

{{< code title="internal/cmd/plug/plug_plugins.go" language="golang" open="true" collapsible="false" copy="true" >}}
for _, p := range rtx.CommandChain()[0].DiscoveredPlugins() {
	fmt.Fprintf(rtx.Stdout, "  %s\t%s\n", p.Name, p.Path)
}
{{< /code >}}

`PluginBinary(name)` reports which binary a plugin would run, searching where dispatch searches.
A failure to run a plugin is a `*rotini.PluginError`, whose `Kind` says whether the binary was
not found, timed out or could not be started. The [spec reference](/specification#pluginspec)
lists every plugin key.

### Be a plugin for kubectl, Docker or Flux

kubectl, Docker and Flux each run plugins as their own sub-commands: `kubectl ctx` runs a binary
named `kubectl-ctx`. A rotini CLI can be one of those plugins. This section covers what each
host expects and the few lines that make a rotini CLI fit.

#### How each host runs a plugin

| | kubectl | Docker | Flux |
|---|---|---|---|
| Binary name | `kubectl-<name>` | `docker-<name>` | `flux-<name>` |
| Where it is found | anywhere on `PATH` | `~/.docker/cli-plugins` (or `$DOCKER_CONFIG/cli-plugins`), plus system plugin directories | `$FLUXCD_PLUGINS`, default `~/.fluxcd/plugins` (Flux 2.9 and later) |
| Arguments it receives | everything after the plugin's name | **Docker's own arguments**: `docker -c prod where x` runs `docker-where -c prod where x` | everything after the plugin's name, plus flags typed before it |
| Before running it | nothing | runs `docker-<name> docker-cli-plugin-metadata` and reads one JSON object | nothing |

For kubectl, each `-` in the binary name is a level of sub-command: `kubectl-ctx-use` answers
`kubectl ctx use`. A `-` inside a single command name is written as `_` in the file name.

**Docker passes its own arguments through**, so a Docker plugin's spec stands in for `docker`
itself. The root accepts Docker's global options (`--config`, `-c`/`--context`, `-D`, `-H`,
`-l`, `--tls…`), marked `cascading: true`, and the plugin is a sub-command beneath it:

{{< code title="cmd/docker-where/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: docker-where
  display_name: docker
  flags:
    - name: context
      identifiers: [-c, --context]
      cascading: true
      schema: { type: string }
    # … the rest of Docker's global options
  commands:
    - name: where
      summary: Show which context and daemon endpoint docker would use
    - name: docker-cli-plugin-metadata
      summary: print the plugin's metadata for docker
      hidden: true
{{< /code >}}

The hidden `docker-cli-plugin-metadata` command answers Docker's probe. Its handler prints one
JSON object and nothing else. Docker refuses a plugin whose `SchemaVersion` is not `0.1.0` or
whose `Vendor` is empty, and lists the plugin in `docker --help` with its `ShortDescription`:

{{< code title="internal/cmd/docker-where/docker_where_docker_cli_plugin_metadata.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*dockerWhereDockerCliPluginMetadataHandler) Run(ctx context.Context, rtx *rotini.Context) {
	meta := map[string]string{
		"SchemaVersion":    "0.1.0",
		"Vendor":           "example",
		"Version":          rtx.Version(),
		"ShortDescription": "Show which context and daemon endpoint docker would use",
	}
	if err := json.NewEncoder(rtx.Stdout).Encode(meta); err != nil {
		rtx.HaltWith(err)
	}
}
{{< /code >}}

#### Tab completion

Each host completes a plugin's arguments by asking the plugin, and all three read the same
completion format: one candidate per line, `value<TAB>description`, then a final `:<number>`
line with directives such as "don't fall back to file names". Rotini computes the answer from
the spec, your completers and each input's `complete:` hint, and `rotini.PluginCompletion`
writes it in that format.

**kubectl** runs a separate executable, `kubectl_complete-<name>`, found on `PATH`. Install the
plugin's binary a second time under that name (a copy or a symlink), and have `main.go` answer
when it is run that way:

{{< code title="cmd/kubectl-ctx/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
func main() {
	if strings.Contains(filepath.Base(os.Args[0]), "_complete-") {
		code, _ := cmd.Program.Complete(os.Args[1:], rotini.PluginCompletion)
		os.Exit(code)
	}
	cmd.Program.WithVersion(version).Execute()
}
{{< /code >}}

**Docker and Flux** run the plugin's own hidden `__complete` command:
`docker-where __complete where <words…>`, `flux-suspended __complete <words…>`. Every rotini CLI
has that command; by default it answers in the format rotini's own generated shell scripts read.
Set the plugin format in `main.go`:

{{< code title="cmd/docker-where/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	WithVersion(version).
	WithCompletion(rotini.PluginCompletion).
	Execute()
{{< /code >}}

Both hosts treat the last line as the directive line, always. Without the setting, the last real
candidate would be read as a directive and lost. Leave it unset for a standalone CLI, whose
completion scripts come from the `completion` feature and expect rotini's format.

#### Help that reads like the host

Set `display_name` on the root, and every generated help, man and markdown page shows the
command the user types instead of the binary's name:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: kubectl-ctx
  display_name: kubectl ctx
{{< /code >}}

`kubectl ctx use --help` then shows `Usage: kubectl ctx use …`. File names, the completion
target and anything you wrote verbatim keep the binary's real name.

#### kubectl's connection flags

kubectl users expect a plugin to take the same connection flags kubectl does: `--kubeconfig`,
`--context`, `-n`/`--namespace`, `--as`, `--token`, `--server` and the rest. Declare them on the
plugin's root with `cascading: true`, so they work on every sub-command and its help lists them,
and hand the parsed values to `ConfigFlags` from `k8s.io/cli-runtime`. It then does everything
kubectl does with them: loading and merging kubeconfigs, choosing the context, impersonation, TLS,
and the namespace default.

{{< code title="internal/cmd/kubectl-ctx/kube.go" language="golang" open="true" collapsible="false" copy="true" >}}
func configFlags(f KubectlCtxFlags) *genericclioptions.ConfigFlags {
	cf := genericclioptions.NewConfigFlags(true)
	*cf.KubeConfig = f.Kubeconfig
	*cf.Context = f.Context
	*cf.Namespace = f.Namespace
	*cf.BearerToken = f.Token
	*cf.Impersonate = f.As
	// … one line per flag
	return cf
}
{{< /code >}}

{{< alert type="warning" title="DON'T BIND KUBECONFIG TO THE FLAG:" >}}
Leave the `--kubeconfig` flag without an environment fallback. `KUBECONFIG` is a list of files,
separated by `:`, that client-go merges. Bound to the flag it would become one path, and a user
with two kubeconfig files would get neither. Left alone, client-go reads it exactly as kubectl
does.
{{< /alert >}}

The short spellings kubectl users type from habit parse as they expect: `-nfoo`, `-lapp=api`,
`-ojson`, and `-A` grouped with other short flags.

Flux's plugins follow the same pattern with Flux's conventions: `-n` falls back to
`$FLUX_SYSTEM_NAMESPACE`, then to `flux-system`, not to the kubeconfig context's namespace.

## Testing

Build a fresh `Program` per test and run it with arguments, in-process: no subprocess and no
`os.Exit`. `Run` returns the exit code and the error the run recorded:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAdd(t *testing.T) {
	var out bytes.Buffer
	code, err := NewProgram(Handlers()).WithStdout(&out).Run([]string{"add", "buy milk"})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if !strings.Contains(out.String(), `added "buy milk"`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestAddRejectsUnknownPriority(t *testing.T) {
	var stderr bytes.Buffer
	code, err := NewProgram(Handlers()).WithStderr(&stderr).Run([]string{"add", "buy milk", "-p", "urgent"})
	if err == nil || code != 1 {
		t.Fatalf("code %d, err %v", code, err)
	}
}
{{< /code >}}

Keep two things in mind:

- **`NewProgram(Handlers())` is not the program `main.go` runs.** Settings `main.go` adds, such as
  a reporter, a version or dependencies, are missing until the test adds them too. With the
  [reporter above](#errors-and-exit-codes), the second test would call
  `NewProgram(Handlers()).WithReporter(Report)` and expect exit code 2.
- **Environment variables and config files come from the process.** Rotini reads them from the
  real environment, so isolate a test with `t.Setenv`, for example
  `t.Setenv("TODO_DEFAULTS_PRIORITY", "high")`, and point the XDG config directory somewhere empty
  with `t.Setenv("XDG_CONFIG_HOME", t.TempDir())`.

## Composing CLIs

A command can be another CLI's spec: `- $ref: ../db/.rotini.spec.yaml` mounts it as a
sub-command, and it still builds and ships on its own. To mount a spec from a module your project
depends on, use `$ref: mod://<module>@<version>/<path>`; it is read from the module cache and
verified by `go.sum`. Keys set next to the `$ref`, such as a new `name`, `summary` or `group`, adjust
it for its new parent; see [`$ref`](/specification#ref).

## Versions

`main.go` passes `version` to `WithVersion`. Stamp it at build time:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go build -ldflags "-X main.version=1.2.3" ./cmd/todo
./todo --version   # 1.2.3
{{< /code >}}

The `version:` key at the top of your spec and conf is the minimum rotini version they need. An
older rotini, or a different major version, refuses to generate from them.
