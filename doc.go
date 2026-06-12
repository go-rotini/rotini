// Package rotini is the runtime for rotini-built command-line programs:
// declare the CLI in a spec file, generate the program with the rotini tool,
// and run it on a slim runtime that does nothing the spec didn't declare —
// everything beyond dispatch is an explicit, opt-in service.
//
// The package rests on four pillars, and this tour reads in their order:
// declare → generate → run → opt in. A complete, buildable program lives in
// examples/ ; the .docs reference files in the repository show every spec and
// conf key with commentary.
//
// # Declare
//
// A CLI is a .rotini.spec.yaml (or json/jsonc/toml) document: the command
// tree, every input — flags, positional arguments, environment variables,
// config-file values, a stdin payload — plus help/man/markdown doc-fields,
// shell completion, and plugin dispatch, all as data. `rotini validate` is
// the gate: the JSON Schema rejects what it can express, rotini-specific
// lint rules reject the rest, and problems name the source line
// (file:line:col for yaml/json/jsonc). Nothing schema-accepted is silently
// ignored — a key either has a consumer or validation rejects it.
//
// # Generate
//
// `rotini generate` compiles the spec into a framework file (the [Definition]
// literal, typed per-command input structs, embedded help/man/markdown pages
// and completion scripts) plus one editable handler stub per command —
// created once, then owned by you. `rotini init` scaffolds a new CLI with a
// wired root/help/version experience; `rotini init --wire completion` (or
// man, markdown) additionally enables that feature and seeds its serving
// command and handler.
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
//   - [Parser] parses and validates the argv channel against the resolved
//     chain — GNU/POSIX grammar (clustering, --, =, count flags, passthrough),
//     typed coercion, enum and constraint checks — into the generated input
//     struct, failing with a data-shaped [*ParseError].
//   - [Binder] reconciles every declared channel — argv, environment, config
//     files ([BindMeta], with discovery, schema gates, and provenance-aware
//     precedence), and the stdin payload — into the same struct.
//   - The overlay surface (ParseArgv / ParseEnv / ParseFiles / ParseStdin,
//     [OverlayInputs], [OverlayInputsP]) acquires channels one at a time for
//     programs that want custom precedence, with a [Report] answering "where
//     did this value come from" per field.
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
