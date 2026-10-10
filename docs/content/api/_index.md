---
title: "api"
---

# api.go

This page maps the parts of the runtime you use in `main.go`, in handlers and in tests. Every
type and method is documented in full on
[pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/rotini).

## In main.go

`main.go` builds the program with the generated `NewProgram(Handlers())`, configures it and calls
`Execute`:

{{< code title="cmd/todo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
func main() {
	cmd.NewProgram(cmd.Handlers()).
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
| `WithEnviron(env)` / `WithDir(dir)` | give a run its own environment and working directory instead of the process's, so tests can run in parallel |
| `WithBufferedOutput(true)` | buffer stdout, flushed and checked before the reporter runs; see [buffered output](/docs#buffered-output) |
| `WithTerminationTimeout(d)` | exit with the signal's code when a run is still going `d` after Ctrl+C or SIGTERM; see [a deadline after a signal](/docs#a-deadline-after-a-signal) |
| `WithArgv0(name)` | set the name a `multicall` program dispatches on, for tests; see [one binary, several names](/docs#one-binary-several-names) |
| `WithContext(ctx)` / `RunContext(ctx, argv)` | run under your own context, which leaves signals to you; see [signals and cancellation](/docs#signals-and-cancellation) |
| `WithSignals(sigs...)` | choose which signals stop the run, also with your own context |
| `WithPanicRecover(b)` / `WithTeardownOnPanic(b)` | re-raise panics, or skip teardown after one; see [panics](/docs#panics) |
| `WithClock(now)` | set the clock relative times such as `2h` and `today` are read against, for tests |
| `WithOutputChecks(true)` | check every structured output against its declared shape before writing it; see [check it](/docs#check-it) |
| `WithCompletion(format)` / `Complete(words, format)` | answer completion requests in another program's format; see [tab completion](/docs#tab-completion) |
| `WithCompletionMessages(fn)` / `WithCompletionDescriptions(fn)` | decide when completion shows messages and descriptions |
| `WithExit(fn)` / `WithArgs(argv)` | replace `os.Exit` and `os.Args` for `Execute`, for an embedding host |
| `With(opts...)` | apply `rotini.Option` values, such as `rotini.WithDependency(dep, v)` |
| `Definition()` | the command tree the program runs, for `rotini.ArgvOf` and your own tools |
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
| `rtx.LookupEnv(name)` / `rtx.Dir()` | the run's environment and working directory; open a relative path with `filepath.Join(rtx.Dir(), path)` |
| `rtx.MustGetDependency(dep)` | a dependency registered in `main.go` |
| `rtx.RecordSuccess` / `rtx.RecordWarning` / `rtx.RecordInfo` | record an outcome; the reporter prints it once, after the teardown hooks |
| `rtx.HaltWith(err)` | fail: record the error and stop |
| `rtx.Failed()` | whether anything has failed so far, for a teardown hook deciding to commit or roll back |
| `rtx.Help()` / `rtx.Version()` | the command's help page and the program's version |
| `rtx.Usage()` | the command's usage line, for a reporter to print after a usage error; see [printing the usage line](/docs#printing-the-usage-line) |
| `rtx.InputsWithReport[T]()` | the inputs plus where each value came from; `report.Format(w)` prints it; see [explaining where values came from](/docs#explaining-where-values-came-from) |
| `rtx.WriteOutput(v, format, render)` | write a command's [structured output](/docs#structured-output) |
| `shape.Render(t)` | a renderer for `WriteOutput` that runs a `--format '{{…}}'` template, from the opt-in package `github.com/go-rotini/rotini/shape`; see [templates](/docs#templates) |
| `rotini.OpenInput(rtx, path)` / `rotini.CreateOutput(rtx, path)` | open an `inputfile` or `outputfile` value, where `-` is stdin or stdout, with `rotini.Overwrite(true)` and `rotini.FileMode(0o600)` as `CreateOutput`'s options; see [files and the standard streams](/docs#files-and-the-standard-streams) |
| `rotini.IsTerminal(rtx.Stdout)` | whether a stream is a terminal, to pick a default format or skip a prompt |
| `rtx.SetDependency(dep, v)` / `rtx.SetDependencyIfAbsent(dep, v)` / `rtx.GetDependency(dep)` | set a dependency for this run only, or read one that may be missing; see [per-run dependencies](/docs#per-run-dependencies) |
| `rtx.SetContext(ctx)` / `rtx.Context()` | hand a derived context, with a deadline or a span, to the hooks after this one; see [passing a context on](/docs#passing-a-context-on) |
| `rtx.ArgvInputs[T]()`, `EnvInputs`, `FileInputs`, `StdinInputs`, `DefaultInputs` | one source's values, to merge with a source of your own with `rotini.MergeInputsWithReport` |
| `rtx.CheckInputs(v, rotini.PresenceOf(v))` | check values you collected yourself against the spec; see [checking inputs you collected yourself](/docs#checking-inputs-you-collected-yourself) |
| `rtx.PartialInputs[T]()` | what has been typed so far, in a completer; see [completers](/docs#completers) |
| `rtx.SetCompletionOptions(rotini.CompletionOptions{…})` | ask the shell, from a completer, to add no space after the candidate (`NoSpace`) or keep the answer's order (`KeepOrder`); see [completers](/docs#completers) |
| `rtx.CommandChain()` / `rtx.CommandPath()` | the commands from the root to the invoked one |
| `rtx.DashIndex()` | how many arguments came before a `--` the user typed |
| `rtx.Now()` | the run's one clock reading, the one relative times are measured from |
| `rotini.Deprecations(rtx)` | the deprecated spellings this run used; see [deprecating a command or flag](/docs#deprecating-a-command-or-flag) |
| `rotini.AppDirs(rtx, app, strategy)` | the config, data, cache and state directories; see [files your program keeps](/docs#files-your-program-keeps) |
| `rotini.ConfigProfiles(rtx, file)` | the profiles a config file defines, for a completer; see [profiles](/docs#profiles) |
| `rotini.SortBy(items, field, desc)` / `rotini.SelectFields(v, at, fields)` | sort a command's output by a field and keep the fields the user chose; see [choosing fields and order](/docs#choosing-fields-and-order) |
| `rtx.WriteOutputTo(w, …)` / `rtx.WriteOutputItem(item, …)` | write structured output to another writer, or one item of a stream |

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

`errors.Is(err, rotini.ErrUsage)` and `errors.Is(err, rotini.ErrInternal)` test the same categories,
and `rotini.ErrDependencyNotFound` is what a `*rotini.DependencyError` unwraps to. The error types are `*rotini.ParseError` (the command line and validation, with a `Kind`),
`*rotini.InputError` (environment, config files and stdin), `*rotini.PluginError`,
`*rotini.WiringError`, `*rotini.DependencyError` and `*rotini.PanicError`; reach them with
`errors.As`. `rotini.SuggestionFacts(err)` returns the word the user typed and what it could have
been, and `rotini.NewSuggestor().For(err)` ranks them; see
[suggesting a correction](/docs#suggesting-a-correction). To end a run with your own code from
outside its hooks, cancel its context with `rotini.ExitCause(code)`; see
[signals and cancellation](/docs#signals-and-cancellation).

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

Build a fresh program per test with `NewProgram(Handlers())`, as `main.go` does. `Run` never
calls `os.Exit`, and the error it returns joins every recorded error, so
`errors.Is` and `errors.As` reach each one; see
[handling errors in a handler](/docs#handling-errors-in-a-handler) for branching on them.

To test a single hook or the input parsing without the generated program, `rotini.NewContextFor`
builds a context from a `rotini.Definition` you write in the test. A `Definition` is the runtime
form of the command tree `rotini generate` produces from your spec. Building one by hand is
supported for tests and for tooling; it is not a way to define a CLI, which the spec is. Fields
are added as the spec gains keys, including in minor releases, so write them with field names
(`rotini.FlagDef{Name: "verbose"}`), not positionally. `Context.WithStdin`, `WithStdout`, `WithStderr`, `WithEnviron`,
`WithDir`, `WithClock` and `WithOutputChecks` set up such a context.

`rotinitest.Run` runs a command from its generated inputs type, each run with its own
environment and directory, so tests can run in parallel. `rotinitest.Output` decodes stdout
against the output schema, and `rotinitest.ExitDocumented` checks the exit code against
`exit_status`; see [testing with typed inputs](/docs#testing-with-typed-inputs). Underneath,
`rotini.ArgvOf` turns an inputs value into argv and env, and `Program.Definition` returns the
command tree it reads.

`DecodeOutput[T](p, stdout, format)` reads captured output back into the command's output type,
checking it against the declared shape.

## Other packages

| Package | What it is |
|---|---|
| `github.com/go-rotini/rotini/shape` | `shape.Template`, a flag type for a `--format '{{…}}'` Go template, and `shape.Render`, the renderer that runs it; see [templates](/docs#templates) |
| `github.com/go-rotini/rotini/rotinitest` | `Run`, `Output`, `OutputAs` and `ExitDocumented` for tests with typed inputs, with the options `Env`, `Dir`, `Stdin`, `Context`, `Presence`, `Secrets` and `Path`; see [testing with typed inputs](/docs#testing-with-typed-inputs) |

Both are opt-in: a program that doesn't import them links none of their code.

## Building blocks

Generated code and most programs never need these, but they are supported:

| Symbol | Use it to |
|---|---|
| `rotini.NewProgramFunc(def, lookup)` | wire a command tree to handlers through a lookup function, without reflection; the generated `NewProgram` uses it, and `rotini.NewProgram(def, handlers)` is the reflection-based form |
| `Program.WithResolver` / `rotini.DefaultResolver` | replace how the command line is resolved to a command or a plugin |
| `Program.WithLifecycle` / `rotini.DefaultLifecycle` / `rotini.AsCommand` | replace which hooks run and in what order |
| `Program.WithParser` / `rotini.NewParser`, `rtx.Parser()` | parse the command line into a struct yourself |
| `Program.WithInputReader` / `rotini.NewInputReader`, `Program.WithInputSettings` | replace how environment, config and stdin are read |
| `Program.WithHelp` | replace where `rtx.Help()` finds a command's page |
| `rotini.MergeInputs` / `rotini.MergeInputsWithReport` | merge input layers, your own included, in precedence order |
| `rotini.ArgvOf` with `rotini.ArgvPath`, `rotini.ArgvSecrets` | turn an inputs value into a command line; see [turning inputs back into a command line](/docs#turning-inputs-back-into-a-command-line) |
| `rotini.StripANSI(text)` | remove ANSI escapes, as Rotini does for man and markdown pages |
| `rotini.Ptr(v)` | a pointer to `v`, for the bounds in a hand-built `rotini.Constraints` |
