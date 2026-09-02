---
title: "api"
---

# Runtime Contract

The full godoc lives in `doc.go`. This page is the shape of it: what a handler is handed, how input arrives, and how a result gets out.

## Program

The generated entrypoint builds a `Program` and calls `Execute`, which resolves the command from argv, runs its lifecycle, and exits.

{{< code title="program" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	Bind(rotini.KeyParser, rotini.NewParser()).       // opt-in services
	Bind(rotini.KeySuggestor, rotini.NewSuggestor()).
	Execute()
{{< /code >}}

`Execute` ends the process. **`Run(argv)` does not** — it returns the exit code instead, which is what makes a `Program` reusable by a REPL, a daemon, or a test driving the whole binary end to end. Each `Run` gets a fresh `Context`, so one invocation never inherits another's outcomes; `RunContext(ctx, argv)` scopes a single invocation without changing the program.

Seams, all optional: `WithStdin`/`WithStdout`/`WithStderr`, `WithArgs`, `WithExit`, `WithContext`, `WithSignals`/`WithoutSignalHandling`, `WithPanicForward`/`WithPanicRecover`, `WithFunnel`, `WithResolver`, `WithLifecycle`.

## Lifecycle

Five hooks per command, sharing one `Context`:

`CascadingPreRun` → `PreRun` → `Run` → `PostRun` → `CascadingPostRun`

Cascading hooks run for every command in the resolved chain, root to leaf, so a parent can set up what its children need. A panic or a deliberate exit halts forward progress while **every begun teardown still runs**. Embed the `Default*` types for hooks you do not implement.

## Context

One per invocation, passed to every hook.

| | |
|---|---|
| `rtx.Args` | the raw argv |
| `rtx.Chain()` | the resolved command path, root → leaf |
| `rtx.Stdin` `rtx.Stdout` `rtx.Stderr` | the program's streams — write through these, never `os.Std*`, and your handler tests cleanly |
| `rtx.Bind` / `Get` / `MustGet` | the service registry |
| `rtx.RecordInfo` / `RecordSuccess` / `RecordWarning` / `RecordError` | outcomes |
| `rtx.SignalExit(code)` / `rtx.Exit(code)` | stop, gracefully or immediately |

## Input

`Collect[T]` is the whole story for most handlers: every declared channel — argv, environment, configuration files, stdin, defaults — reconciled and validated in one line.

{{< code title="input" language="golang" open="true" collapsible="false" copy="true" >}}
inputs, err := rotini.Collect[TodoAddInputs](rtx)

// CollectP adds provenance: which channel won, per field.
inputs, report, err := rotini.CollectP[TodoAddInputs](rtx)

// Or take one channel at a time, for custom precedence.
argv, _ := rotini.ParseArgv[TodoAddInputs](rtx)
env,  _ := rotini.ParseEnv[TodoAddInputs](rtx)
merged  := rotini.OverlayInputs(argv, env)
{{< /code >}}

## Outcomes

{{< alert type="info" title="RECORD, DON'T PRINT:" >}}
A handler does not print its results or its errors. It **records** them, and the runtime reports them once, after teardown, through a single funnel. A generated handler therefore carries zero reporting code — and a program that wants different reporting changes one function instead of every command.
{{< /alert >}}

Five channels reach the funnel: infos, successes, warnings, errors, and panics (recovered panics plus rotini-detected faults — there is no record call for those). The default funnel prints each with a severity label and applies a conservative exit floor: a recorded error exits non-zero unless a deliberate code was already set, which it never downgrades.

`WithFunnel` replaces all of it, receiving every channel at once so cross-channel logic, print order and the exit code live in one place.

## Errors

Every class is both `errors.Is`-able against the `ErrUsage` / `ErrInternal` sentinels — so `CategoryOf` classifies it — and `errors.As`-able to a typed value with structured fields. rotini's own messages never leak internals or secret values.

| Type | Channel |
|---|---|
| `*ParseError` | argv — with a `Kind` you can branch on, plus the token and candidates a `Suggestor` turns into "did you mean" |
| `*BindError` | environment / configuration / stdin |
| `*RemoteError` | plugin dispatch |
| `*WiringError` `*ServiceError` `*PanicError` | rotini-detected faults, arriving as panics |

rotini ships **no opinions on top**: no automatic "did you mean", no help dump on error. A program that wants either writes its own funnel.

## Opt-in services

Nothing is wired unless the generated code — or you — binds it. `KeyParser`, `KeyBinder`, `KeySuggestor`, `KeyStyler` and `KeyBindMeta` name the usual suspects: `Parser` (argv alone), `Binder` (`Collect`'s engine), `Suggestor` (string-distance matching), and `Style`/`Styler` (SGR styling by intent, with `Strip`, `Width` and `Hyperlink`).

Detection is opt-in too: `DetectProfile`, `IsTerminal` and `EnvNoColor` exist, but nothing calls them for you — the program decides and feeds the result in.
