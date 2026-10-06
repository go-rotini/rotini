---
title: "api"
---

# api.go

This page maps the parts of the runtime you use in `main.go`, in handlers and in tests. Every
type and method is documented in full on
[pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).

## In main.go

The generated package exports a ready `Program`. `main.go` configures it and calls `Execute`:

{{< code title="cmd/todo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
func main() {
	cmd.Program.
		WithDependency(cmd.Store, openStore()). // something your handlers share
		WithVersion(version).                   // what rtx.Version() returns
		Execute()                               // runs the command, then exits with its code
}
{{< /code >}}

| Method | Use it to |
|---|---|
| `WithVersion(v)` | set the version `rtx.Version()` returns, which the `--version` flag and `version` command that `rotini init` declares print |
| `WithDependency(dep, v)` | give handlers a dependency, such as a store or an API client |
| `WithStdin` / `WithStdout` / `WithStderr` | replace the program's streams |
| `WithReporter(fn)` | replace how recorded outcomes are printed and which exit code is used |
| `WithoutSignalHandling()` | turn off the default Ctrl+C and SIGTERM handling |
| `Execute()` | run with `os.Args` and exit with the resulting code |
| `Run(argv)` | run once and return the exit code and error instead of exiting, for tests and embedding |

A dependency is declared once in the generated package's directory, next to your handlers, and
read in any hook:

{{< code title="internal/cmd/todo/store.go" language="golang" open="true" collapsible="false" copy="true" >}}
var Store = rotini.NewDependency[*store.Store]("todo.store")
{{< /code >}}

## In a handler

Each command's handler has five hooks, which run in this order:

`CascadingPreRun` → `PreRun` → `Run` → `PostRun` → `CascadingPostRun`

`PreRun`, `Run` and `PostRun` run only for the command that was invoked. A command's cascading
hooks also run when any command beneath it is invoked: `CascadingPreRun` from the root down,
`CascadingPostRun` from the invoked command back up. So a parent can set up what its
sub-commands need and tear it down afterward. The teardown hooks, `PostRun` and
`CascadingPostRun`, run even after a halt or a panic, for exactly the hooks whose setup ran.

A generated handler embeds `rotini.NoCascadingPreRun`, `rotini.NoPreRun`, `rotini.NoPostRun`
and `rotini.NoCascadingPostRun`, which do nothing. To use a hook, declare the method on your
handler type.

Every hook receives a `*rotini.Context`, conventionally named `rtx`:

| Call or field | What it gives you |
|---|---|
| `rtx.Inputs[T]()` | the command's inputs from the command line, environment, config files, stdin and defaults, typed and validated |
| `rtx.Stdout` / `rtx.Stderr` / `rtx.Stdin` | the program's streams; write to these, not `os.Stdout`, so tests can capture output |
| `rtx.Argv` | the raw arguments, for a handler that parses its own |
| `rtx.MustGetDependency(dep)` | a dependency registered in `main.go` |
| `rtx.RecordSuccess` / `rtx.RecordWarning` / `rtx.RecordInfo` | record an outcome; the reporter prints it once, after the teardown hooks |
| `rtx.HaltWith(err)` | fail: record the error and stop |
| `rtx.Failed()` | whether anything has failed so far, for a teardown hook deciding to commit or roll back |
| `rtx.Help()` / `rtx.Version()` | the command's help page and the program's version |
| `rtx.WriteOutput(v, format, render)` | write a command's [structured output](/docs#structured-output) |

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoAddHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TodoAddInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := rtx.MustGetDependency(Store).Add(in.TodoAdd.Arguments.Title); err != nil {
		rtx.HaltWith(err)
		return
	}
	rtx.RecordSuccess("task added")
}
{{< /code >}}

## Stopping

| To | Call |
|---|---|
| fail and stop | `rtx.HaltWith(err)` |
| record an error and keep going | `rtx.RecordError(err)` |
| stop without failing | `rtx.Halt()` |
| stop with a specific exit code | `rtx.HaltWithCode(n)` |
| stop now, skipping the teardown hooks | `rtx.Exit(n)` |

Each call stops the hooks that have not started yet; it does not end the function you call it
from, so `return` after it. The first non-zero exit code set wins.

## Errors

By default each recorded error is printed to stderr as `Error: …`, each warning as
`Warning: …`, and the program exits 1 when anything failed, unless a handler already set a
non-zero code.

Mark an error as the user's to fix with `rotini.UsageError(err)`, or as a fault in the program
with `rotini.InternalError(err)`. An unknown flag or a bad value is already a usage error.
`rotini.CategoryOf(err)` returns `rotini.CategoryUsage`, `rotini.CategoryInternal` or
`rotini.CategoryNone`, so a custom reporter can map each to its own exit code.

A custom reporter is a `rotini.Reporter`, `func(ctx context.Context, rtx *rotini.Context, out
rotini.Outcome)`, installed with `Program.WithReporter`. It replaces the default entirely: it
prints only what it prints, and sets the exit code with `rtx.Exit`, so a failed run exits 0
unless it does. The [guide](/docs#errors-and-exit-codes) has a complete one. For scripts,
`rotini.StructuredReporter` writes each outcome to stderr as one JSON object per line.

## Testing

Run a program in-process and check its exit code, error and output:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAdd(t *testing.T) {
	var out bytes.Buffer
	code, err := NewProgram(Handlers()).WithStdout(&out).Run([]string{"add", "buy milk"})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
}
{{< /code >}}

Build a fresh program per test with `NewProgram(Handlers())`. The `With*` methods change the
program they are called on, so configuring the shared `Program` in one test would leak into the
others. `Run` never calls `os.Exit`, and the error it returns joins every recorded error, so
`errors.Is` and `errors.As` reach each one; see
[handling errors in a handler](/docs#handling-errors-in-a-handler) for branching on them.
