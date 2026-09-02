// Package rotini is the runtime for rotini-built command-line programs:
// declare the CLI in a spec file, generate the program with the rotini tool,
// and run it on a slim runtime that does nothing the spec didn't declare —
// everything beyond dispatch is an explicit, opt-in service.
//
// rotini is two-faced, and one module serves both faces at one version:
//
//   - As a TOOL — `go get -tool github.com/go-rotini/rotini` — it installs the
//     codegen binary (`go tool rotini init` / `generate` / `validate`), which
//     compiles your spec into a per-CLI framework file plus one editable handler
//     stub per command.
//   - As a LIBRARY — `go get github.com/go-rotini/rotini` — it is THIS package: the
//     runtime that generated code imports and your handlers are written against.
//
// Generated code is small and yours; the runtime is an ordinary versioned import.
// The symbols below ([Program], [Context], [Collect], the [Handlers] hooks, …) are
// exactly what a handler author uses, qualified `rotini.`.
//
// The package rests on four pillars, and this tour reads in their order:
// declare → generate → run → opt in. The companion CLI (cmd/rotini, built
// with rotini itself) is the worked example; the annotated reference files under
// reference/ show every spec and conf key with commentary.
//
// # Declare
//
// A CLI is a .rotini.spec.yaml (or json/jsonc/toml) document: the command
// tree, every input channel — argv flags and positional arguments, the
// flag value sentinels (from: file's @path, from: stdin's -), environment
// variables (explicit, derived under env_prefix, or nested families) and
// flags' env fallbacks, configuration files (fixed, walked-up, xdg, or
// config_source-supplied), a typed stdin payload, and declared defaults —
// plus help/man/markdown doc-fields, shell completion, and plugin dispatch,
// all as data. `rotini validate` is the gate: the JSON Schema rejects what it can express, rotini-specific
// lint rules reject the rest, and problems name the source line
// (file:line:col for yaml/json/jsonc). Nothing schema-accepted is silently
// ignored — a key either has a consumer or validation rejects it.
//
// # Generate
//
// `rotini generate` compiles the spec into a framework file (the [Definition]
// literal, typed per-command input structs, embedded help/man/markdown pages
// and completion scripts) plus one editable handler stub per command —
// created once, then owned by you. `rotini init` scaffolds a new CLI as a
// minimal skeleton: a root-only spec, a conf with every feature off, the
// entrypoint, and one empty handler stub. You grow it from there — each
// feature (help, completion, man, markdown) is a conf toggle you turn on.
//
// # Composition
//
// rotini is commands all the way down: a command is composable at any node, so a
// CLI is assembled from specs the way its tree is assembled from commands. A
// "$ref" pulls another spec in as a subcommand. Five modes span where a command's
// spec and its handler code come from, from wholly-owned to wholly-remote:
//
//  1. Standalone — an own spec node with its own generated handler stub. The
//     default; every leaf you write by hand.
//  2. Inline + passthrough — an own spec node whose structure and typed inputs are
//     generated locally (exactly like Standalone), but whose handler CODE is sourced
//     from a package via handler: { import: <alias path>, convention: <Name> }
//     instead of a stub — the own-types + delegated-handler hybrid; codegen emits
//     alias.<Convention>() and seeds no stub. Per-command: there is no subtree
//     cascade, so an inline sub-command without its own handler: still gets a normal
//     stub. The handler package imports the generated cmd package for its input
//     types.
//  3. Local composition — a "$ref" to a sibling spec in the same module. The
//     child's tree merges in (on an overlapping key the parent wins), and codegen
//     auto-delegates each composed command to the child's generated package
//     (childcli.Handlers().X()). Same-module only.
//  4. Module composition — a "$ref" to mod://<module>@<version>/<path>, resolved
//     through the Go module cache and pinned by go.sum. The child is another
//     module's spec; its handlers auto-delegate from THAT module's generated
//     package — composition across a module boundary with no extra wiring.
//  5. Remote command — a sibling binary <program>-<name>, dispatched at RUNTIME
//     (remote_commands / remote_discovery), not composed at codegen: the spec tree
//     links the command and the runtime spawns the binary, reporting a dispatch
//     failure as a [*RemoteError]. Discovery (remote_discovery) dispatches an
//     unmatched token to <prefix><token> the same way.
//
// `rotini validate` follows refs and collision-checks the whole assembled tree,
// so a duplicate name, a cycle, or a missing ref is caught before codegen, and
// generate and validate stay hermetic — rotini has no fetcher: a local ref reads the
// filesystem, a mod:// ref reads the Go module cache (pinned by go.mod/go.sum), and a
// git:: or raw https:// ref is refused outright.
//
// # The slim runtime
//
// The generated entrypoint builds a [Program] with [NewProgram] and calls
// [Program.Execute]: resolve the invoked command from argv, run its
// lifecycle hooks ([Handlers] — CascadingPreRun/PreRun/Run/PostRun/
// CascadingPostRun, halting on a panic or a deliberate exit while every
// begun teardown still runs), and exit. Each invocation carries a [Context]:
// the argv ([Context.Args]), the resolved chain ([Context.Chain]), the
// program's streams (Stdin/Stdout/Stderr — configurable via
// [Program.WithStdin] and friends, so handlers test cleanly), and the
// service registry. Exits are deliberate ([Context.SignalExit],
// [Context.Exit]); a recorded error, a recovered panic, or a rotini-detected
// fault is reported once, after teardown, through the outcome funnel (see
// Outcomes below), and exits 1 by default. The runtime's only built-in
// behaviors, documented as the exceptions they are: a default SIGINT/SIGTERM
// trap (controllable via [Program.WithoutSignalHandling] / [Program.WithSignals],
// or deferred to the caller by [Program.WithContext]), the hidden __complete
// entry the generated shell scripts call, and os.Exit as the default exit action
// (capture it with [Program.WithExit]).
//
// # Outcomes
//
// A run reports through ONE outcome funnel ([Program.WithFunnel]), handed all five
// recorded channels at once, fired once after the lifecycle settles, with a sane
// default. A handler does not print — it RECORDS, and the runtime reports:
//
//   - [Context.RecordInfo] (a message) — neutral informational output.
//   - [Context.RecordSuccess] (a message) — what went right.
//   - [Context.RecordWarning] (an error) — non-fatal: a deprecation, a fallback.
//     Never changes the exit code.
//   - [Context.RecordError] (an error) — the end-user's own failures: a bad input,
//     a domain error.
//   - A recovered panic, or a rotini-DETECTED fault (a [*WiringError] from a
//     [Definition] vs. handlers mismatch, a resolver fault, a [MustGet] on a
//     missing service) — rotini's "this should never have happened". There is NO
//     record call: the lifecycle captures it, and the funnel receives it as its
//     panics slice.
//
// The funnel signature is [FunnelFunc]: fn(ctx, rtx, infos, successes, warnings,
// errors, panics). Recording is non-halting — a handler records any number of times
// across any hook, then stops independently with [Context.SignalExit] (graceful,
// teardown runs) or [Context.Exit] (skip teardown), or simply returns. The funnel
// fires only when SOME channel is non-empty; a run that records nothing and never
// faults does not invoke it (a silent success). The opt-in model holds: an outcome
// is reported only because the handler chose to record it (panic excepted — it is
// the runtime's final fallback so a recovered panic still prints clean, never a raw
// stack).
//
// Two orthogonal knobs tune the panic path; a CLI keeps both defaults.
// [Program.WithPanicForward] (default true) decides whether teardown runs after a
// hook panics — true unwinds every begun hook's teardown (cleanup on failure, like
// a `defer`), false hard-stops. [Program.WithPanicRecover] (default true) decides
// where the panic lands — true funnels it (so consumers never see a raw stack),
// false re-raises it raw to the caller. They compose: recover=false with
// forward=true runs teardown THEN re-panics; recover=false with forward=false
// panics immediately, with the original stack.
//
// Exit code: a handler's explicit [Context.SignalExit] or [Context.Exit] carries
// into the funnel (first non-zero). The funnel is the FINAL authority — it runs in
// the funnel stage where [Context.Exit] OVERRIDES that code, so a custom funnel owns
// the exit entirely (it may even exit 0 on a panic). The DEFAULT funnel applies the
// floor conservatively: a recorded error or a captured fault exits non-zero (1)
// unless a deliberate code is already set, which it never downgrades. Info, success,
// and warning never change the code. rotini holds no named exit-code constants — a
// CLI that wants other codes (e.g. a category-based map) sets them in its funnel.
//
// The default funnel prints in the order info → warning → error → panic → success —
// infos and successes to stdout, the rest to stderr — one clean severity-labeled
// line per item (a [*PanicError]'s stack rides along for an errors.As but is never
// printed). A custom funnel gets everything together, so it controls cross-channel
// logic, print order, and the exit code in ONE place. So a generated handler carries
// ZERO reporting code: it records and stops, and the runtime reports.
//
// Every error and fault class is both [errors.Is]-able against the [ErrUsage] /
// [ErrInternal] sentinels (so [CategoryOf] classifies it) and [errors.As]-able
// to a typed value with structured fields — and rotini's own messages are
// non-leaky (no recon/decode/OS internals, no secret values):
//
//   - [*ParseError] — the argv channel (unknown flag, bad value/enum/arity).
//     [ParseError.Kind] ([ParseKind]) branches it without matching the message;
//     Token + Candidates are the raw material a Suggestor turns into "did you
//     mean". Recorded as an error.
//   - [*BindError] — the env / config / stdin / flag-fallback channels: carries
//     the Channel, the Input, and a clean message, with the recon Cause
//     reachable via errors.As. Recorded as an error.
//   - [*RemoteError] — a plugin dispatch (binary-not-found / timeout / spawn, by
//     [RemoteErrorKind]). Recorded as an error: a missing plugin is the consumer's
//     environment, not the engineer's fault.
//   - [*WiringError] (a [Definition] vs. handlers mismatch), [*ServiceError] (a
//     [MustGet] miss), and [*PanicError] (any recovered panic) arrive as panics.
//
// rotini ships no opinions on top: no "did you mean", no help dump on error. A
// program that wants either writes its own funnel — e.g. one that ranges its
// recorded errors and applies the bound [Suggestor] to a [*ParseError] Token,
// renders help for [ParseError.Command], logs, or redacts. Suggestion is the
// program's call, never the framework's (Pillar 1).
//
// # Opt-in services
//
// Everything else is a value a handler fetches from the registry —
// [Program.Bind] to provide, [Get] / [MustGet] to consume, with the [KeyParser],
// [KeyBinder], [KeySuggestor], [KeyStyler] conventions naming
// the usual suspects:
//
//   - [Collect] is the 95% handler's whole input story: every declared
//     channel reconciled and validated in one line —
//     inputs, err := rotini.Collect[DeployInputs](rtx) — and
//     [CollectP] adds the provenance [Report] ("where did this value come
//     from", per field). Both ride the [BindMeta] the generated NewProgram
//     binds under [KeyBindMeta].
//   - [Parser] parses and validates the argv channel ALONE against the
//     resolved chain — GNU/POSIX grammar (clustering, --, =, count flags,
//     passthrough), typed coercion, enum and constraint checks — failing
//     with a data-shaped [*ParseError]; [Binder] is Collect's engine, for
//     callers who want to hold the meta explicitly.
//   - The per-channel surface ([ParseArgv] / [ParseEnv] / [ParseFiles] /
//     [ParseStdin] / [Defaults], composed by [OverlayInputs] or
//     [OverlayInputsP]) acquires channels one at a time for programs that
//     want custom precedence, with the same [Report].
//   - [Suggestor] turns a [ParseError]'s unknown token and candidate
//     vocabulary into "did you mean" suggestions.
//   - Text styling — a [Style] fluent builder
//     for SGR styling (16/256/RGB/hex color, with opt-in Profile downsampling),
//     a Styler registry of named styles (render text by intent — "warning",
//     "error"), plus Strip, Hyperlink, Width, and opt-in DetectProfile /
//     IsTerminal / EnvNoColor helpers. A disabled Style adds nothing (passing
//     text through); a disabled Styler strips, so a program-wide "no color"
//     comes out clean. Detection is opt-in — the program decides and feeds the
//     result in; rotini auto-detects nothing, and there are no
//     tables/prompts/progress (the deleted UX layer stays deleted).
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve
//     and orchestration phases wholesale; [FlagValueCompleter] and
//     [ArgValueCompleter] feed dynamic completion candidates.
//
// None of these are wired unless the generated code — or yours — binds them;
// the spec declares, the generator writes it down, and the runtime does
// exactly that.
//
// # Batteries
//
// Beyond the runtime, rotini carries the pieces a one-shot, interactive, or
// long-running binary keeps needing. They are batteries on a shelf: importing
// rotini wires none of them, starts no goroutine, and touches no terminal —
// each does something only because your handler constructed it and called it.
//
//   - [Printer] is the "data out" complement to [Collect]'s "data in": one writer
//     that renders a value as text, JSON, YAML, TOML or a table, chosen from
//     whatever your --output flag carried ([ParseFormat] turns the flag's string
//     into a [Format]). It pairs with the spec's command output: key, which
//     generates the typed <Prefix>Output struct — the spec declares the shape, the
//     Printer renders it, and neither wires a flag.
//   - [Table] renders aligned columns, measuring cells by DISPLAY width ([Width]),
//     so styled and wide-rune text line up. Optionally bounded to a width budget
//     and optionally styled through a [Styler].
//   - [Prompt], [Confirm] and [Select] ask questions. They read from an
//     [io.Reader], so the same code works interactively, from a pipe
//     (echo y | mycli), and in a test — and input that ends without an answer is
//     [ErrNotInteractive] rather than a hang. [Select] is a numbered menu (no raw
//     terminal mode, so it works over a pipe) and resolves a typed answer by
//     number, by exact text, or — with [Select.WithSuggestor] — by fuzzy match.
//   - [Spinner] and [Progress] show live activity. Both redraw ONE line in place,
//     and both stay silent on a non-terminal writer so a CI log is never smeared
//     with carriage returns (the one place rotini detects anything by default —
//     see the [Spinner] docs for why, and WithAnimation to override).
//   - [Pager] sends long output through $PAGER, and passes it straight through
//     when there is no terminal — so a piped invocation is never hijacked.
//   - [Subprocess] wraps [os/exec] with environment, working directory and
//     timeout control; a non-zero exit is a [*SubprocessError] quoting the child's
//     stderr, and [Subprocess.Lines] streams tagged output as an iterator you can
//     break out of.
//
// # Program shapes
//
// A rotini binary is not always a one-shot command. These run the SAME program in
// a different shape, and all of them rest on [Program.Run] / [Program.RunContext]
// being re-entrant — each dispatch gets a fresh [Context], so nothing leaks
// between invocations while the services bound once up front reach all of them.
//
//   - [REPL] runs a [Program] as an interactive loop: each typed line is
//     tokenized like a shell command line and dispatched against the same
//     [Definition] the binary uses, so every command, flag and handler behaves
//     identically. A failing command is reported and the loop continues; the
//     session ends on an exit word, end of input, or a done context.
//   - [Service] runs long-lived workers until the context ends or one fails, with
//     ordered shutdown hooks that run in every case. Because the runtime already
//     cancels the run context on SIGINT/SIGTERM, a handler that builds a Service
//     on its own ctx gets signal-driven graceful shutdown for free.
//   - [Scheduler] is [Service] with timers: interval tasks with optional jitter,
//     so a fleet started together does not stampede in lockstep.
//   - [StdioServer] serves JSON-RPC 2.0 over stdin/stdout, in newline-delimited
//     or Content-Length framing — how LSP language servers and MCP servers speak.
//     It is the shape a CLI takes when a tool drives it instead of a human.
//   - [Wizard] sequences steps into a guided flow with branching ([WizardStep.When])
//     and back navigation ([ErrWizardBack]). It owns no streams: a step does its own
//     asking with a [Prompt], [Select] or [Confirm], which keeps the flow pure
//     orchestration and testable with plain funcs.
//
// For watching files and for a single-instance lock, use go-rotini/fs
// (fs.NewWatcher, fs.PIDLock); for caching in a long-running program, go-rotini/
// memcache. rotini does not reimplement them.
package rotini
