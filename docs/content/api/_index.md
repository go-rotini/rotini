---
title: "api"
---

# Runtime Contract

The full godoc lives in `doc.go`. This page is the shape of it: what a handler is handed, how input arrives, and how a result gets out.

## Program

The generated entrypoint builds a `Program` and calls `Execute`, which resolves the command from argv, runs its lifecycle, and exits.

{{< code title="program" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	WithVersion(version).                    // what --version reports
	WithSuggestor(rotini.NewSuggestor()).    // an opt-in seam
	Execute()
{{< /code >}}

`Execute` ends the process. **`Run(argv)` does not** — it returns the exit code instead, which is what makes a `Program` reusable by a REPL, a daemon, or a test driving the whole binary end to end. Each `Run` gets a fresh `Context`, so one invocation never inherits another's outcomes; `RunContext(ctx, argv)` scopes a single invocation without changing the program.

{{< alert type="info" title="EXECUTE RETURNS AN ERROR YOU USUALLY CANNOT SEE:" >}}
It is the run's own failure — every recorded error and every recovered fault, joined, so `errors.Is` and `errors.As` reach each one.

But it arrives **only when the exit action returns**. Under the default, `os.Exit`, the process is gone before the return statement runs, which is why the generated entrypoint discards it. Install a `WithExit` that returns — a test, or a host embedding the CLI — and it arrives.

It is not the reporting channel: the funnel has already printed everything by then. The return is there so an embedder can *act* on the failure rather than re-derive it from a stream.
{{< /alert >}}

Seams, all optional: `WithStdin`/`WithStdout`/`WithStderr`, `WithArgs`, `WithExit`, `WithContext`, `WithSignals`/`WithoutSignalHandling`, `WithTeardownOnPanic`/`WithPanicRecover`, `WithFunnel`, `WithResolver`, `WithLifecycle`, `WithVersion`/`WithParser`/`WithStyler`/`WithSuggestor`/`WithBindMeta`/`WithBinder`, and `With` for options that cannot be methods.

## Lifecycle

Five hooks per command, sharing one `Context`:

`CascadingPreRun` → `PreRun` → `Run` → `PostRun` → `CascadingPostRun`

Cascading hooks run for every command in the resolved chain, root to leaf, so a parent can set up what its children need. A panic or a deliberate exit halts forward progress while **every begun teardown still runs**. Embed the `Default*` types for hooks you do not implement.

## Context

One per invocation, passed to every hook.

| | |
|---|---|
| `rtx.Argv` | the raw argument vector — command names and flags included, unparsed. Not to be confused with a command's **declared positionals**, which are `inputs.X.Arguments`, already parsed and typed |
| `rtx.Chain()` | the resolved command path, root → leaf — a **copy**, so editing it cannot reach the run |
| `rtx.Stdin` `rtx.Stdout` `rtx.Stderr` | the program's streams — write through these, never `os.Std*`, and your handler tests cleanly |
| `rtx.Command()` / `rtx.CommandPath()` | the command the user **invoked** (the leaf), and its full path |
| `rtx.Frame()` | the command whose **hook is running** — the leaf in `Run`, an ancestor in a cascading hook |
| `rtx.Bind` / `rtx.Get[T]` / `rtx.MustGet[T]` | the service registry (generic methods, Go 1.27) |
| `rtx.RecordInfo` / `RecordSuccess` / `RecordWarning` / `RecordError` | outcomes |
| `rtx.Failed()` | has anything failed so far — the one bit a teardown needs |
| `rtx.HaltWith(err)` | fail: record and stop, in one call — see below |
| `rtx.Halt()` / `rtx.HaltWithCode(code)` / `rtx.Exit(code)` | stop — see below |

### Failing, and stopping

A hook has no return value, so failing is something you *say* rather than something you return. One call says it:

{{< code title="the one you want, in any hook" language="golang" open="true" collapsible="false" copy="true" >}}
if err := store.Save(task); err != nil {
	rtx.HaltWith(err) // records it, stops the run; the funnel decides the cost
	return
}
{{< /code >}}

Each intent has one spelling, and nothing is taken away:

| Intent | Call |
|---|---|
| **fail here, stop the run** | `HaltWith(err)` — `RecordError` + `Halt` as one act, claiming no exit code |
| record a problem and **keep going** — collect several, or let a later hook decide | `RecordError(err)` on its own |
| stop cleanly, nothing failed | `Halt()` |
| stop **and** claim a code, when the number is the point — a filter reporting "no match" as 1, a wrapper passing a child's status through | `HaltWithCode(n)` |
| stop now and **skip pending teardown**, when remaining cleanup must not run | `Exit(n)` |

One rule covers the four: **everything named `Halt*` leaves teardown intact; `Exit` does not** — which is why it is spelled like `os.Exit`, whose deferred functions do not run either.

{{< alert type="warning" title="HALT STOPS FORWARD PROGRESS — WHICH IS ONLY TWO OF THE FIVE HOOKS:" >}}
| Hook | What `Halt()` does there |
|---|---|
| `CascadingPreRun` | **stops the run** — no further setup, no `PreRun`, no `Run` |
| `PreRun` | **stops the run** — no `Run` |
| `Run` | nothing: `Run` is the last forward step |
| `PostRun` | nothing: the unwind runs to completion |
| `CascadingPostRun` | nothing |

So `Halt` is how a **setup** hook refuses to let a command proceed, and its failure mode is omission. A setup hook that records an error and returns *without* halting exits with the same code and the same stderr as one that halts — the only difference is that the command went on to do the work its setup had just established it must not do.

`HaltWith` is the answer: it is correct in all five hooks, so failing never depends on knowing which one you are in, and one call cannot be half-written.
{{< /alert >}}

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

**A handler collects the type generated for its own command, in any hook.** That is the whole rule, and there is only one call.

An inputs type describes a command and its ancestors: its last field is the collecting command, the fields before it are that command's lineage. `Collect` anchors the struct on `rtx.Frame()` — the command whose hook is running — so the same call is correct in a leaf's `Run`, in a cascading hook three frames up, and in a composed child mounted under someone else's umbrella.

{{< code title="a cascading hook reading its own flags — the same call a leaf uses" language="golang" open="true" collapsible="false" copy="true" >}}
func (*rootHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	// Anchored on this command, not the leaf, because that is whose hook this is.
	in, err := rotini.Collect[MycliInputs](rtx)
	...
}
{{< /code >}}

{{< alert type="info" title="COMMAND() IS THE LEAF; FRAME() IS WHOSE HOOK YOU ARE IN:" >}}
For `mig db status`, `rtx.Command()` is `status` in **every** hook of the run — it is the command the user invoked. `rtx.Frame()` is the command this particular hook belongs to: `mig` in mig's cascading hook, `db` in db's, `status` in the leaf's `PreRun`/`Run`/`PostRun`.

A cascading hook had no way to ask that question before, which is why an inputs struct used to be aligned by counting its fields against the chain — and why a composed child's cascading hook could not read its own flags at all. Its type spans only its own lineage, so counting from the leaf landed below it and counting from the root landed above it. Both returned zeros with a nil error.
{{< /alert >}}

A struct that describes more commands than the collecting command is deep is rejected — that check is exact, because the anchor is known rather than inferred. `Binder.BindRoot` remains as a low-level escape for a caller that genuinely wants the first *n* frames.

## Outcomes

{{< alert type="info" title="RECORD, DON'T PRINT:" >}}
A handler does not print its results or its errors. It **records** them, and the runtime reports them once, after teardown, through a single funnel. A generated handler therefore carries zero reporting code — and a program that wants different reporting changes one function instead of every command.
{{< /alert >}}

Five channels reach the funnel, together in one `Outcome`: `Infos`, `Successes`, `Warnings`, `Errors`, and `Panics` (recovered panics plus rotini-detected faults — there is no record call for those). The default funnel prints each with a severity label and applies a conservative exit floor: a recorded error exits non-zero unless a deliberate code was already set, which it never downgrades.

`WithFunnel` replaces all of it, receiving every channel at once so cross-channel logic, print order and the exit code live in one place.

{{< code title="a custom funnel" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.WithFunnel(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
    for _, e := range out.Errors {
        fmt.Fprintf(rtx.Stderr, "%s: %v\n", rtx.CommandPath(), e)
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

**The registry is yours alone.** `Bind` and `Provide` write to it; nothing rotini depends on lives there.

That is a deliberate split. rotini's own seams used to be string keys in the same flat namespace — `"parser"`, `"version"`, `"binder"` — with nothing reserving them and every read discarding its comma-ok, so a name you chose or a type you got wrong degraded an input channel in silence. The worst case was reachable by accident: binding a `Binder` built from an empty `BindMeta`, the only way it could be written without knowing about a key you had never seen, switched the configuration-file channel off without a word.

| rotini's seam | supply | read back |
|---|---|---|
| the generated descriptor | `WithBindMeta(meta)` — the generated `NewProgram` does this | internal |
| the binder | `WithBinder(func(BindMeta) *Binder)` — it **receives** the descriptor | internal |
| the version | `WithVersion(v)` | `rtx.Version()` |
| the parser | `WithParser(p)` | `rtx.Parser()` — never nil |
| the styler | `WithStyler(s)` | `rtx.Styler() (*Styler, bool)` |
| the suggestor | `WithSuggestor(s)` | `rtx.Suggestor() (*Suggestor, bool)` |

`WithBinder` takes a function of the descriptor rather than a `*Binder` for exactly that reason — an override now starts *from* what the spec declared instead of having to reproduce it.

Styling and suggestion report `(value, ok)` because both are opt-in: rotini styles nothing and suggests nothing on its own, so "none supplied" is a decision worth telling a handler about rather than papering over with a default it never asked for.

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
	WithVersion(version).
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
