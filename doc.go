// Package rotini is the runtime for rotini-built command-line programs: declare the CLI in a
// spec file, generate the program with the rotini tool, and run it on a runtime that does
// nothing the spec did not declare — everything beyond dispatch is an explicit, opt-in service.
//
// One module serves two faces at one version:
//
//   - As a tool — `go get -tool github.com/go-rotini/rotini` — it installs the codegen binary
//     (`go tool rotini init` / `generate` / `validate`), which compiles a spec into a per-CLI
//     framework file plus one editable handler stub per command.
//   - As a library — `go get github.com/go-rotini/rotini` — it is this package: the runtime
//     that generated code imports and handlers are written against.
//
// The companion CLI under cmd/rotini is built with rotini itself and is the worked example;
// the annotated files under reference/ show every spec and conf key with commentary.
//
// # Declare
//
// A CLI is a .rotini.spec.yaml (or json, jsonc, toml) document: the command tree, every input
// channel — argv flags and positional arguments, the value sentinels (from: file's @path,
// from: stdin's -), environment variables and flags' env fallbacks, configuration files, a
// typed stdin payload, declared defaults — plus doc fields, shell completion and plugin
// dispatch, all as data.
//
// `rotini validate` is the gate: the JSON Schema rejects what it can express and lint rules
// reject the rest, with problems named to file:line:col. Nothing schema-accepted is silently
// ignored — a key either has a consumer or validation rejects it.
//
// # Generate
//
// `rotini generate` compiles the spec into a framework file — the [Definition] literal, typed
// per-command input structs, embedded help/man/markdown pages and completion scripts — plus
// one handler stub per command, created once and then yours. `rotini init` scaffolds a working
// CLI — a spec declaring -h/--help, -v/--version and the conventional help and version
// commands, a conf with the help feature on and the other three off, an entrypoint, and a stub
// per command already wired to the pages and services codegen produced. Every line of it is
// yours to delete; the remaining features are conf toggles you turn on from there.
//
// # Composition
//
// rotini is commands all the way down: a command is composable at any node, so a CLI is
// assembled from specs the way its tree is assembled from commands. Five modes span where a
// command's spec and its handler code come from:
//
//  1. Standalone — an own spec node with its own generated stub. The default.
//  2. Inline with handler passthrough — an own spec node whose structure and typed inputs are
//     generated locally, but whose handler code comes from a package named by
//     handler: { import: …, convention: … }. Codegen emits the delegating call and seeds no
//     stub. It does not cascade: an inline sub-command without its own handler still gets one.
//  3. Local composition — a "$ref" to a sibling spec in the same module. The child's tree
//     merges in, the parent winning on an overlapping key, and each composed command
//     auto-delegates to the child's generated package.
//  4. Module composition — a "$ref" to mod://<module>@<version>/<path>, resolved through the
//     Go module cache and pinned by go.sum, delegating to that module's generated package.
//  5. Remote command — a sibling binary <program>-<name>, dispatched at run time rather than
//     composed at codegen; a dispatch failure is a [*RemoteError]. Discovery dispatches an
//     unmatched token to <prefix><token> the same way.
//
// `rotini validate` follows refs and collision-checks the assembled tree, so a duplicate name,
// a cycle or a missing ref is caught before codegen. Generation is hermetic: rotini has no
// fetcher, a local ref reads the filesystem, a mod:// ref reads the module cache, and a git::
// or raw https:// ref is refused.
//
// # The runtime
//
// The generated entrypoint builds a [Program] with [NewProgram] and calls [Program.Execute]:
// resolve the invoked command from argv, run its [Handlers] hooks, and exit. Each invocation
// carries a [Context] — the argv, the resolved chain, the program's streams, and the service
// registry. Stopping is deliberate ([Context.Halt], [Context.HaltWithCode], [Context.Exit]), and a recorded error,
// recovered panic or detected fault is reported once, after teardown, through the outcome
// funnel.
//
// The runtime's only built-in behaviors, documented as the exceptions they are: a default
// SIGINT/SIGTERM trap (see [Program.WithoutSignalHandling] and [Program.WithSignals]), the
// hidden __complete entry the generated shell scripts call, and os.Exit as the default exit
// action (capture it with [Program.WithExit]).
//
// # Slices at the boundary
//
// One rule, because the two directions differ and the difference has bitten:
//
//   - A slice rotini RETURNS is a copy. [Context.Chain] and every [Outcome] channel hand back
//     their own, so sorting, reslicing or editing one cannot reach the run. Chain used to be
//     the live slice with a doc asking callers to treat it as read-only, and a single
//     assignment through it silently rewrote [Context.CommandPath] for the rest of the invocation.
//
//   - A slice you PASS IN is kept, not copied. [Program.WithArgs], [Program.WithSignals] and
//     the slices inside a [BindMeta] are held by reference, so mutating yours afterwards
//     changes the program. Copying them defensively would cost every caller for a mistake
//     almost nobody makes; saying so costs nothing.
//
// [Context.Argv] is the deliberate exception in the first group: it is documented as the live
// argv precisely so a handler can run its own parser over it.
//
// # Outcomes
//
// A run reports through one funnel ([Program.WithFunnel]), handed all five recorded channels
// at once as an [Outcome], fired once after the lifecycle settles. A handler does not print —
// it records, and the runtime reports:
//
//   - [Context.RecordInfo] — neutral informational output.
//   - [Context.RecordSuccess] — what went right.
//   - [Context.RecordWarning] — non-fatal: a deprecation, a fallback. Never changes the code.
//   - [Context.RecordError] — the end-user's own failures: a bad input, a domain error.
//   - Recovered panics and rotini-detected faults. There is no record call: the lifecycle
//     captures them, and the funnel receives them as its panics slice.
//
// Recording is non-halting: a handler records any number of times across any hook, then stops
// independently, or simply returns. The funnel fires only when some channel is non-empty, so a
// run that records nothing is a silent success.
//
// There are three ways to stop, and which one to reach for is decided by whether the exit code
// is the point:
//
//   - [Context.Halt] stops forward progress and claims NO code, leaving the verdict to what the
//     run recorded and to the funnel. This is the commonest stop — a handler that has recorded
//     an error and has nothing further to do — and the one to prefer when a program centralizes
//     its exit policy in a funnel.
//   - [Context.HaltWithCode] stops AND claims a code, for when the number is the point: a filter
//     reporting "no match" as 1, a wrapper passing a child's status through.
//   - [Context.Exit] stops immediately and skips pending teardown, for when remaining cleanup
//     must not run.
//
// Halting matters as much as recording. A hook that records a failure and returns without
// stopping lets the next hook collect the same inputs, hit the same validation and record the
// same error again.
//
// The default funnel prints info → warning → error → panic → success, infos and successes to
// stdout and the rest to stderr, then applies the exit floor: a recorded error or fault exits
// 1 unless a handler already set a deliberate code, which it never downgrades. The funnel is
// the final authority, so a custom one owns the exit entirely. rotini holds no named exit-code
// constants.
//
// Every error class is errors.Is-able against the [ErrUsage] and [ErrInternal] sentinels, so
// [CategoryOf] classifies it, and errors.As-able to a typed value with structured fields.
// rotini's own messages are non-leaky — no recon, decode or OS internals, and no secret values:
//
//   - [*ParseError] — the argv channel. [ParseError.Kind] branches it without matching the
//     message; Token and Candidates are what a [Suggestor] turns into "did you mean".
//   - [*BindError] — the env, config, stdin and flag-fallback channels, carrying the channel,
//     the input and a clean message, with the recon cause reachable via errors.As.
//   - [*RemoteError] — a plugin dispatch. Recorded as an error: a missing plugin is the
//     consumer's environment, not the author's fault.
//   - [*WiringError], [*ServiceError] and [*PanicError] arrive as panics.
//
// rotini ships no opinions on top: no "did you mean", no help dump on error. A program that
// wants either writes its own funnel.
//
// # Sharing dependencies between handlers
//
// The store, client or logger every handler needs rides the registry, reached by a typed [Key]
// so the name and the type cannot drift apart:
//
//	// declared once, beside the thing it names
//	var StoreKey = rotini.NewKey[Store]("store")
//
//	// main.go — the value's type is checked here, where it is supplied
//	tasks.StoreKey.Provide(cmd.Program, tasks.NewStore()).Execute()
//
//	// or, for several at once, without leaving the chain ([Provide] and [Program.With])
//	cmd.Program.
//		With(
//			rotini.Provide(tasks.StoreKey, tasks.NewStore()),
//			rotini.Provide(tasks.ClientKey, tasks.NewClient()),
//		).
//		WithVersion(version).
//		Execute()
//
//	// any handler — no string, no type assertion, no miss check
//	store := tasks.StoreKey.MustGet(rtx)
//
// For a one-off lookup, rtx.Get[T](key) and rtx.MustGet[T](key) supply the type at the call
// site. A handler that needs to know which command it is asks the context: [Context.Command] is
// the resolved leaf, [Context.CommandPath] the canonical invocation ("tasks add"), and
// [Context.Chain] the full chain with the tokens the user actually typed.
//
// # Opt-in services
//
// Everything else is a value a handler fetches from the registry — [Program.Bind] to provide,
// [Context.Get] or [Context.MustGet] to consume.
//
// rotini's OWN seams are not in that registry. [Program.WithBindMeta], [Program.WithBinder],
// [Program.WithParser], [Program.WithSuggestor] and [Program.WithVersion] supply them;
// [Context.Parser], [Context.Suggestor] and [Context.Version] read them back. The registry is yours alone, so nothing rotini depends
// on can be shadowed by a name you chose or a type you got wrong:
//
//   - [Collect] is the typical handler's whole input story: every declared channel reconciled
//     and validated in one line, into the command's generated inputs type.
//
//     inputs, err := rotini.Collect[DeployInputs](rtx)
//
//     [CollectP] adds the provenance [Report]. Both ride the [BindMeta] the generated
//     NewProgram supplies via [Program.WithBindMeta].
//
//   - [Parser] parses and validates the argv channel alone — GNU/POSIX grammar, typed
//     coercion, enum and constraint checks — failing with a [*ParseError]. [Binder] is
//     Collect's engine, for callers who want to hold the meta explicitly. Neither needs
//     supplying to be used: [Collect] builds its own, and [Program.WithParser] overrides it.
//
//   - [Deprecations] reports the deprecated aliases and identifiers this invocation actually
//     used. It is a plain function over the [Context] and needs no service bound.
//
//   - The per-channel surface ([ParseArgv], [ParseEnv], [ParseFiles], [ParseStdin],
//     [Defaults], composed by [OverlayInputs] or [OverlayInputsP]) acquires channels one at a
//     time, for programs that want custom precedence.
//
//   - [Suggestor] turns a [ParseError]'s unknown token and candidate vocabulary into "did you
//     mean" suggestions.
//
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve and orchestration
//     phases wholesale; [FlagValueCompleter] and [ArgValueCompleter] feed dynamic completion.
//
// None of these are wired unless the generated code — or yours — binds them.
//
// # Batteries
//
// Beyond the runtime, rotini carries a short shelf of things a binary keeps needing that are
// awkward to write and easy to get wrong. Importing rotini wires none of them, starts no
// goroutine and touches no terminal.
//
// The shelf is deliberately SHORT. rotini ships no styler, no table, no spinner, no prompt and
// no pager, because drawing to a terminal is a solved problem with better libraries behind it
// than a CLI framework should be writing on the side. What stays here is the part underneath
// those choices: platform questions the standard library will not answer, and process work that
// is subtly wrong in most hand-rolled versions.
//
//   - [Subprocess] wraps os/exec with environment, working-directory and timeout control; a
//     non-zero exit is a [*SubprocessError] quoting the child's stderr, and [Subprocess.Lines]
//     streams tagged output as an iterator you can break out of.
//   - [TerminalSize] reports the terminal's width and height, honoring COLUMNS and LINES and
//     saying plainly when there is no answer rather than inventing one. [IsTerminal] and
//     [EnvNoColor] answer the two questions that come before any styling decision; rotini
//     auto-detects nothing.
//   - [ReadSecret] reads one line with terminal echo off and puts the echo back on every path,
//     including a panic — the failure nobody notices until their next shell command.
//   - [Strip] removes ANSI escape sequences, which is what makes a styled string safe to put
//     in a man page, a markdown page or a completion description.
//
// # Program shapes
//
// A rotini binary is not always a one-shot command. These run the same program in a different
// shape, all resting on [Program.Run] being re-entrant — each dispatch gets a fresh [Context],
// so nothing leaks between invocations while services bound once up front reach all of them.
// Run is also safe to call CONCURRENTLY once configuration is done; the handlers value and the
// program's streams stay shared, so a concurrent host synchronizes those. See [Program.Run].
//
//   - [REPL] runs a [Program] as an interactive loop, dispatching each typed line against the
//     same [Definition] the binary uses. A failing command is reported and the loop continues.
//   - [Service] runs long-lived workers until the context ends or one fails, with ordered
//     shutdown hooks that run in every case. Since the runtime already cancels the run context
//     on SIGINT/SIGTERM, a Service built on that ctx gets graceful shutdown for free.
//   - [Scheduler] is [Service] with timers: interval tasks with optional jitter, so a fleet
//     started together does not stampede in lockstep.
//
// # What rotini deliberately does not ship
//
// Some of it lives elsewhere in the same ecosystem:
//
//   - watching files — go-rotini/fs, fs.NewWatcher
//   - a single-instance lock — go-rotini/fs, fs.PIDLock
//   - caching in a long-running program — go-rotini/memcache
//
// Import them directly. A re-export would give each API two names, put its documentation in
// the wrong package, and pull another module's surface inside rotini's compatibility promise.
//
// The rest is not rotini's to ship at all. Styling, tables, spinners, prompts, forms and
// paging are how a program DRAWS, and that is a design decision belonging to the program and
// to libraries built for it. A framework that shipped its own would either be worse than they
// are or grow into a second product; either way its users would end up with two vocabularies
// for the same screen. rotini's job is turning a spec into a parsed, bound, dispatched
// invocation, and handing your handler a [Context] that knows what the user asked for. What
// the handler prints, and how, is yours.
package rotini
