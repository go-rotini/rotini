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
// [Context.Exit]); framework failures and recovered panics funnel — exactly
// once, with the panic's stack riding a [*PanicError] — into
// [Program.WithErrorFn], where [CategoryOf] maps them onto conventional
// codes ([ExitUsage], [ExitInternal]). The runtime's only built-in
// behaviors, documented as the exceptions they are: a default SIGINT/SIGTERM
// trap (suppressed by [Program.WithContext]), the hidden __complete entry
// the generated shell scripts call, and os.Exit as the default exit action
// (capture it with [Program.WithExit]).
//
// # Opt-in services
//
// Everything else is a value a handler fetches from the registry —
// [Program.Bind] to provide, [Get] / [MustGet] to consume, with the [KeyParser],
// [KeyBinder], [KeySuggestor], [KeyVersioner] conventions naming the usual
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
//   - [Suggestor] turns a [ParseError]'s unknown token and candidate
//     vocabulary into "did you mean" suggestions; [Versioner] resolves one
//     version string whether the binary was built with -ldflags or installed
//     by module path.
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve
//     and orchestration phases wholesale; [FlagValueCompleter] and
//     [ArgValueCompleter] feed dynamic completion candidates.
//
// None of these are wired unless the generated code — or yours — binds them;
// the spec declares, the generator writes it down, and the runtime does
// exactly that.
package rotini
