// Package rotini is the runtime for rotini-built command-line programs:
// declare the CLI in a spec file, generate the program with the rotini tool,
// and run it on a slim runtime that does nothing the spec didn't declare —
// everything beyond dispatch is an explicit, opt-in service.
//
// The package rests on four pillars, and this tour reads in their order:
// declare → generate → run → opt in. The companion CLI (cmd/rotini, built
// with rotini itself) is the worked example; the .docs reference files in
// the repository show every spec and conf key with commentary.
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
// minimal skeleton — everything is opt-in: `--with help` seeds the -h flags,
// help command, and wired handlers; `--with version`, `--with completion`,
// `--with man`, `--with markdown` (or `--with all`) each wire their feature,
// commands, and handlers the same way.
//
// # The slim runtime
//
// The generated entrypoint builds a [Program] with [NewProgram] and calls
// [Program.Execute]: resolve the invoked command from argv, run its
// lifecycle hooks ([CommandHandlers] — CascadingPreRun/PreRun/Run/PostRun/
// CascadingPostRun, halting on a panic or a deliberate exit while every
// begun teardown still runs), and exit. Each invocation carries a [Context]:
// the argv ([Context.Args]), the resolved chain ([Context.Chain]), the
// program's streams (Stdin/Stdout/Stderr — configurable via
// [Program.WithStdin] and friends, so handlers test cleanly), and the
// service registry. Exits are deliberate ([Context.SignalExit],
// [Context.Exit]); a recorded error, a recovered panic, or a rotini-detected
// fault is reported once, after teardown, through the outcome funnels (see
// Outcomes below), and exits 1 by default. The runtime's only built-in
// behaviors, documented as the exceptions they are: a default SIGINT/SIGTERM
// trap (controllable via [Program.WithoutSignalHandling] / [Program.WithSignals],
// or deferred to the caller by [Program.WithContext]), the hidden __complete
// entry the generated shell scripts call, and os.Exit as the default exit action
// (capture it with [Program.WithExit]).
//
// # Outcomes
//
// A run reports through FOUR outcome funnels, each fed by its own channel, each
// fired once after the lifecycle settles, each with a sane default. A handler
// does not print — it RECORDS, and the runtime reports:
//
//   - [Context.RecordError] (an error) reaches [Program.WithOnErrorFn] — the
//     end-user's own failures: a bad input, a domain error.
//   - [Context.RecordWarning] (an error) reaches [Program.WithOnWarningFn] —
//     non-fatal: a deprecation, a fallback. Never changes the exit code.
//   - [Context.RecordSuccess] (a message) reaches [Program.WithOnSuccessFn] —
//     what went right.
//   - A recovered panic, or a rotini-DETECTED fault (a [*WiringError] from a
//     [Definition] vs. handlers mismatch, a resolver fault, a [MustGet] on a
//     missing service), reaches [Program.WithOnPanicFn] — rotini's "this should
//     never have happened". There is NO record call: the lifecycle captures it,
//     and the funnel receives the captured faults as its slice argument.
//
// Recording is non-halting — a handler records any number of times across any
// hook, then stops independently with [Context.SignalExit] (graceful, teardown
// runs) or [Context.Exit] (skip teardown), or simply returns. Each funnel fires
// only when its channel is non-empty, in the fixed order warning, success, error,
// panic; a run that records nothing and never faults fires none of them (a
// silent success). The opt-in model holds: an outcome is reported only because
// the handler chose to record it (panic excepted — it is the runtime's final
// fallback so a recovered panic still prints clean, never a raw stack).
//
// Two orthogonal knobs tune the panic path; a CLI keeps both defaults.
// [Program.WithPanicForward] (default true) decides whether teardown runs after a
// hook panics — true unwinds every begun hook's teardown (cleanup on failure, like
// a `defer`), false hard-stops. [Program.WithPanicRecover] (default true) decides
// where the panic lands — true funnels it to OnPanic so consumers never see a raw
// stack, false re-raises it raw to the caller. They compose: recover=false with
// forward=true runs teardown THEN re-panics; recover=false with forward=false
// panics immediately, with the original stack.
//
// Exit code: a handler's (or a funnel's) explicit [Context.SignalExit] or
// [Context.Exit] wins (first non-zero). The two floors differ by owner. The ERROR
// floor belongs to the DEFAULT OnError funnel — it calls [Context.SignalExit](1),
// so a recorded error exits 1 by default, but a CUSTOM [Program.WithOnErrorFn] that
// omits SignalExit may legitimately exit 0 (the end-user's own error channel is the
// end-user's policy). The FAULT floor belongs to the RUNTIME and is not overridable:
// a recovered panic or a rotini-detected fault always exits non-zero, never the
// funnel's to mask to 0. Success and warning never change the code. rotini holds no
// named exit-code constants — a CLI that wants other codes (e.g. a category-based
// map) sets them in its funnels.
//
// The defaults print one clean severity-labeled line per item — successes to
// stdout, warnings ("Warning:"), errors ("Error:") and faults ("Fatal Error:") to
// stderr — and a [*PanicError]'s stack rides along for an errors.As but is never
// printed. So a generated handler carries ZERO reporting code: it records and
// stops, and the runtime
// reports.
//
// Every error and fault class is both [errors.Is]-able against the [ErrUsage] /
// [ErrInternal] sentinels (so [CategoryOf] classifies it) and [errors.As]-able
// to a typed value with structured fields — and rotini's own messages are
// non-leaky (no recon/decode/OS internals, no secret values):
//
//   - [*ParseError] — the argv channel (unknown flag, bad value/enum/arity).
//     [ParseError.Kind] ([ParseKind]) branches it without matching the message;
//     Token + Candidates are the raw material a Suggestor turns into "did you
//     mean". Reaches OnError.
//   - [*BindError] — the env / config / stdin / flag-fallback channels: carries
//     the Channel, the Input, and a clean message, with the recon Cause
//     reachable via errors.As. Reaches OnError.
//   - [*RemoteError] — a plugin dispatch (binary-not-found / timeout / spawn, by
//     [RemoteErrorKind]). Recorded as an error, so it reaches OnError: a missing
//     plugin is the consumer's environment, not the engineer's fault.
//   - [*WiringError] (a [Definition] vs. handlers mismatch), [*ServiceError] (a
//     [MustGet] miss), and [*PanicError] (any recovered panic) reach OnPanic.
//
// rotini ships no opinions on top: no "did you mean", no help dump on error. A
// program that wants either writes its own funnel — e.g. a [Program.WithOnErrorFn]
// that ranges its recorded errors and applies the bound tortellini.Suggestor to a
// [*ParseError] Token, renders help for [ParseError.Command], logs, or redacts.
// Suggestion is the program's call, never the framework's (Pillar 1).
//
// # Opt-in services
//
// Everything else is a value a handler fetches from the registry —
// [Program.Bind] to provide, [Get] / [MustGet] to consume, with the [KeyParser],
// [KeyBinder], tortellini.KeySuggestor, [KeyVersioner] conventions naming the usual
// suspects:
//
//   - [Collect] is the 95% handler's whole input story: every declared
//     channel reconciled and validated in one line —
//     inputs, err := rotini.Collect[cmdgen.DeployInputs](rtx) — and
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
//   - tortellini.Suggestor turns a [ParseError]'s unknown token and candidate
//     vocabulary into "did you mean" suggestions; [Versioner] resolves one
//     version string whether the binary was built with -ldflags or installed
//     by module path.
//   - Text styling lives in the tortellini subpackage: a Styler / Style fluent
//     builder for SGR styling, and StripStyles to remove styling from already-
//     styled text (a help page's spec-authored ANSI). Whether to style is the
//     program's call — rotini ships no terminal/NO_COLOR detection, and no
//     tables/prompts/progress (the deleted UX layer stays deleted).
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve
//     and orchestration phases wholesale; [FlagValueCompleter] and
//     [ArgValueCompleter] feed dynamic completion candidates.
//
// None of these are wired unless the generated code — or yours — binds them;
// the spec declares, the generator writes it down, and the runtime does
// exactly that.
package rotini
