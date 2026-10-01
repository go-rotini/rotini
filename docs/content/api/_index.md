---
title: "api"
---

# api.go

The parts of the runtime you will use in `main.go` and your handler files. The complete API is
on [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).

## In main.go

The generated package exports a ready `Program`. `main.go` configures it and calls `Execute`:

{{< code title="cmd/todo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
func main() {
	cmd.StoreKey.Provide(cmd.Program, openStore()) // a service your handlers share
	cmd.Program.
		WithVersion(version). // what --version prints
		Execute()             // runs the command, then exits with its code
}
{{< /code >}}

| Method | Use it to |
|---|---|
| `WithVersion(v)` | set what `--version` and `version` print |
| `Key.Provide(p, v)` / `Bind(key, v)` | give handlers a service (a store, a client) |
| `WithStdin` / `WithStdout` / `WithStderr` | redirect the program's streams |
| `WithFunnel(fn)` | replace how outcomes are reported and which exit code is used |
| `WithoutSignalHandling()` | turn off the default Ctrl-C / SIGTERM handling |
| `Execute()` | run with `os.Args` and exit |
| `Run(argv)` | run once and return the exit code instead of exiting — for tests |

## In a handler

Each command's handler has five hooks, run in this order; embed the `Default*` types for the
ones you don't need:

`CascadingPreRun` → `PreRun` → `Run` → `PostRun` → `CascadingPostRun`

The cascading hooks run for the command and every sub-command beneath it, so a parent can set
up what its children need. Every hook receives a `*rotini.Context`:

| | |
|---|---|
| `rotini.Collect[T](rtx)` | the command's inputs — argv, env, config files, stdin and defaults — typed and validated |
| `rtx.Stdout` / `rtx.Stderr` / `rtx.Stdin` | the streams; write to these, not `os.Stdout` |
| `Key.MustGet(rtx)` / `rtx.MustGet[T](key)` | a service bound in `main.go` |
| `rtx.RecordSuccess` / `RecordWarning` / `RecordInfo` | report an outcome, printed once after the command finishes |
| `rtx.HaltWith(err)` | fail: record the error and stop |
| `rtx.Failed()` | whether anything has failed yet — for a teardown deciding to commit or roll back |
| `rtx.Help()` / `rtx.Version()` | the command's help page, and the program's version |

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoAddHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rotini.Collect[TodoAddInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := StoreKey.MustGet(rtx).Add(in.TodoAdd.Arguments.Title); err != nil {
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
| stop cleanly | `rtx.Halt()` |
| stop with a specific exit code | `rtx.HaltWithCode(n)` |
| stop now, skipping teardown hooks | `rtx.Exit(n)` |

## Errors

By default each recorded error is printed as `Error: …` and the program exits 1. Mark an error
as the user's to fix with `rotini.UsageError(err)`; `rotini.CategoryOf(err)` tells a funnel which
kind it has, so a program can map usage and internal errors to different exit codes.

## Testing

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
var out bytes.Buffer
code, err := NewProgram(Handlers()).WithStdout(&out).Run([]string{"add", "buy milk"})
{{< /code >}}

Build a fresh program per test with `NewProgram(Handlers())`: the `With*` methods change the
program they are called on.
