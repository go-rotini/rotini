---
title: "api"
---

# Runtime Contract

The full godoc lives in `doc.go`. This page is the shape of it: what a handler is handed, how input arrives, and how a result gets out.

## Program

The generated entrypoint builds a `Program` and calls `Execute`, which resolves the command from argv, runs its lifecycle, and exits.

{{< code title="program" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	Bind(rotini.KeyVersion, version).                 // what --version reports
	Bind(rotini.KeySuggestor, rotini.NewSuggestor()). // opt-in services
	Execute()
{{< /code >}}

`Execute` ends the process. **`Run(argv)` does not** — it returns the exit code instead, which is what makes a `Program` reusable by a REPL, a daemon, or a test driving the whole binary end to end. Each `Run` gets a fresh `Context`, so one invocation never inherits another's outcomes; `RunContext(ctx, argv)` scopes a single invocation without changing the program.

{{< alert type="info" title="EXECUTE RETURNS AN ERROR YOU USUALLY CANNOT SEE:" >}}
It is the run's own failure — every recorded error and every recovered fault, joined, so `errors.Is` and `errors.As` reach each one.

But it arrives **only when the exit action returns**. Under the default, `os.Exit`, the process is gone before the return statement runs, which is why the generated entrypoint discards it. Install a `WithExit` that returns — a test, or a host embedding the CLI — and it arrives.

It is not the reporting channel: the funnel has already printed everything by then. The return is there so an embedder can *act* on the failure rather than re-derive it from a stream.
{{< /alert >}}

Seams, all optional: `WithStdin`/`WithStdout`/`WithStderr`, `WithArgs`, `WithExit`, `WithContext`, `WithSignals`/`WithoutSignalHandling`, `WithTeardownOnPanic`/`WithPanicRecover`, `WithFunnel`, `WithResolver`, `WithLifecycle`, and `With` for options that cannot be methods.

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
| `rtx.Command()` / `rtx.Path()` | the command being run, and its full path |
| `rtx.Bind` / `rtx.Get[T]` / `rtx.MustGet[T]` | the service registry (generic methods, Go 1.27) |
| `rtx.RecordInfo` / `RecordSuccess` / `RecordWarning` / `RecordError` | outcomes |
| `rtx.Failed()` | has anything failed so far — the one bit a teardown needs |
| `rtx.Halt()` / `rtx.SignalExit(code)` / `rtx.Exit(code)` | stop — see below |

### Stopping

Three ways, chosen by whether the exit code is the point:

| | |
|---|---|
| `Halt()` | stop, claim **no** code. The verdict is left to what the run recorded and to the funnel. This is the common case — a handler that recorded an error and has nothing more to do. |
| `SignalExit(n)` | stop **and** claim a code, for when the number is the point: a filter reporting "no match" as 1, a wrapper passing a child's status through. |
| `Exit(n)` | stop immediately and **skip** pending teardown, for when remaining cleanup must not run. |

All three leave teardown intact except `Exit`. Halting matters as much as recording: a hook that records a failure and returns *without* stopping lets the next hook collect the same inputs, hit the same validation, and record the same error again.

`Failed()` is what makes a teardown decision possible — commit or roll back, keep or discard:

{{< code title="the decision a teardown exists to make" language="golang" open="true" collapsible="false" copy="true" >}}
func (*migrateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	if rtx.Failed() { // panics count, which is the case a hand-kept flag misses
		tx.Rollback()
		return
	}
	tx.Commit()
}
{{< /code >}}

The `Outcome` a funnel receives answers the same question — but a funnel runs *after* every teardown has finished, which is the right place to report a failure and far too late to undo one.

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

An inputs type binds to the **end** of the command chain: field *i* of an *n*-field struct takes `chain[len(chain)-n+i]`. A handler collecting the type generated for its own command is always right — and so is a composed child's handler, whose shorter type counts back from the leaf and lands on its own frames whatever tree it was grafted into.

The one place that rule cannot serve is a **cascading hook on the root command**: it runs for every invocation, so it cannot name the leaf's type, and its own type would count back from the leaf and land on some descendant. `CollectRoot` anchors at the root instead.

{{< code title="a root cascading hook reading its own flags" language="golang" open="true" collapsible="false" copy="true" >}}
func (*rootHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	in, err := rotini.CollectRoot[MycliInputs](rtx) // not Collect
	...
}
{{< /code >}}

Collecting an ancestor's type with plain `Collect` from a deeper command is rejected rather than silently filled with zeros.

## Outcomes

{{< alert type="info" title="RECORD, DON'T PRINT:" >}}
A handler does not print its results or its errors. It **records** them, and the runtime reports them once, after teardown, through a single funnel. A generated handler therefore carries zero reporting code — and a program that wants different reporting changes one function instead of every command.
{{< /alert >}}

Five channels reach the funnel, together in one `Outcome`: `Infos`, `Successes`, `Warnings`, `Errors`, and `Panics` (recovered panics plus rotini-detected faults — there is no record call for those). The default funnel prints each with a severity label and applies a conservative exit floor: a recorded error exits non-zero unless a deliberate code was already set, which it never downgrades.

`WithFunnel` replaces all of it, receiving every channel at once so cross-channel logic, print order and the exit code live in one place.

{{< code title="a custom funnel" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.WithFunnel(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
    for _, e := range out.Errors {
        fmt.Fprintf(rtx.Stderr, "%s: %v\n", rtx.Path(), e)
    }
    if out.Failed() {
        rtx.Exit(2) // the funnel is the final authority on the exit code
    }
}).Execute()
{{< /code >}}

The channels arrive as a struct rather than as five parameters for a reason that matters to code written against a frozen v1: the two `[]string` channels and the two `[]error` channels cannot be silently transposed at a call site, and a sixth channel added later is an additive field instead of a breaking change to every custom funnel in existence.

## Errors

Every class is both `errors.Is`-able against the `ErrUsage` / `ErrInternal` sentinels — so `CategoryOf` classifies it — and `errors.As`-able to a typed value with structured fields. rotini's own messages never leak internals or secret values.

| Type | Channel |
|---|---|
| `*ParseError` | argv — with a `Kind` you can branch on, plus the token and candidates a `Suggestor` turns into "did you mean" |
| `*BindError` | environment / configuration / stdin |
| `*RemoteError` | plugin dispatch |
| `*WiringError` `*ServiceError` `*PanicError` | rotini-detected faults, arriving as panics |

rotini ships **no opinions on top**: no automatic "did you mean", no help dump on error. A program that wants either writes its own funnel.

## Plugins

A sub-command can be **another binary**. Declare it in the spec and rotini locates it, passes all three streams through, honours a timeout, and returns the plugin's own exit code untouched.

Two kinds, and the difference decides whose fault a missing binary is:

| | in help when missing? | missing means | category |
|---|---|---|---|
| `remote_commands` — **declared** | yes; it is published interface | a broken install | `CategoryInternal` |
| `remote_discovery` — **discovered** `yourcli-*` | no; it appears only when present | a typo | `CategoryUsage` |

A timeout is neither party's fault, so it is `CategoryNone` — still reachable with `errors.As` as a `*RemoteError` whose `Kind` is `RemoteTimeout`.

Both kinds search the same three places in the same order: next to the host binary (the git/kubectl convention), then the command's `plugin_path`, then `PATH`.

{{< code title="what rotini hands you, and what you render" language="golang" open="true" collapsible="false" copy="true" >}}
root := rtx.Chain()[0]

rotini.DiscoveredPlugins(root)      // tokens found now, minus any shadowing a real command
rotini.DiscoveryDiagnostics(root)   // why the configured plugin_path could not be scanned
rotini.RemoteBinaryPath(root, name) // would this resolve, and to what — searched as dispatch searches
{{< /code >}}

rotini renders none of it. Generated help lists the **declared** remotes, because those are known at build time; a handler that wants to list what is installed *now* asks for it. `RemoteBinaryPath` exists because `exec.LookPath` only sees `PATH` — it reports every conventionally installed plugin as missing.

A discovered plugin may never shadow a declared command, so dropping a binary on `PATH` cannot take over part of a CLI's published interface.

## Services

Nothing is wired unless the generated code — or you — binds it. `KeyParser`, `KeyBinder`, `KeySuggestor`, `KeyStyler`, `KeyBindMeta` and `KeyVersion` name the built-ins: `Parser` (argv alone), `Binder` (`Collect`'s engine), `Suggestor` (string-distance matching), `Style`/`Styler` (SGR styling by intent, with `Strip`, `Width` and `Hyperlink`), and the version string `--version` reports.

Your own services use a **typed key**, so the registry string and the type it was bound as cannot drift apart and a handler needs neither a literal nor an assertion:

{{< code title="a typed key" language="golang" open="true" collapsible="false" copy="true" >}}
// declared once, beside the thing it names
var StoreKey = rotini.NewKey[*Store]("store")

// main.go — the type is checked here, where the value is supplied
StoreKey.Provide(cmd.Program, NewStore()).Execute()

// any handler — no string, no assertion, no comma-ok
store := StoreKey.MustGet(rtx)
{{< /code >}}

Go does not allow type parameters on methods, so `p.Provide[T](key, value)` cannot exist — which is why `Key.Provide` takes the program instead of chaining off it. For more than one service, `Provide` returns an option and **`With`** applies any number without leaving the chain:

{{< code title="several services, one expression" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	With(
		rotini.Provide(StoreKey, NewStore()),
		rotini.Provide(ClientKey, NewClient()),
	).
	Bind(rotini.KeyVersion, version).
	Execute()
{{< /code >}}

Options apply left to right, so a later one overwrites an earlier one binding the same key. An `Option` is just a `func(*Program)`, so a program can bundle its own configuration into one value and pass it around.

`Provide` binds program-wide, so every invocation sees it — including every line of a REPL session. `BindTo` binds on one `Context`, for something a hook computes per run.

Detection is opt-in too: `DetectProfile`, `IsTerminal` and `EnvNoColor` exist, but nothing calls them for you — the program decides and feeds the result in.

`Parser` and `Binder` are overrides, not prerequisites: `Collect` builds its own. In particular **`rotini.Deprecations(rtx)` needs nothing bound** — it reports the deprecated aliases and identifiers this invocation actually used, reading the resolved chain and the argv that produced it straight off the `Context`.

{{< code title="reporting a deprecated spelling" language="golang" open="true" collapsible="false" copy="true" >}}
for _, d := range rotini.Deprecations(rtx) {
	replacement := d.Name
	if d.Kind == "flag" {
		replacement = "--" + d.Name
	}
	rtx.RecordWarning(fmt.Errorf("%s; use %s", d, replacement))
}
{{< /code >}}

rotini prints none of it: which deprecations cost a warning, a telemetry event or nothing at all is the program's call.
