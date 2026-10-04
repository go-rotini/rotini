// Package rotini is the runtime for rotini-built command-line programs: declare the CLI in a
// spec file, generate the program with the rotini tool, and run it on a runtime that does
// nothing the spec did not declare — everything beyond dispatch is an explicit, opt-in service.
//
// One module serves two faces at one version:
//
//   - As a tool — `go get -tool github.com/go-rotini/rotini/cmd/rotini@latest` — it installs
//     the codegen binary (`go tool rotini init` / `generate` / `validate`), which compiles a
//     spec into a per-CLI framework file plus one editable handler file per command.
//   - As a library — `go get github.com/go-rotini/rotini` — it is this package: the runtime
//     that generated code imports and handlers are written against.
//
// The companion CLI under cmd/rotini is built with rotini itself and is the worked example;
// docs/assets/examples/ holds a spec and a conf that use every key.
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
//  5. Declared plugin — a sibling binary <program>-<name>, dispatched at run time rather than
//     composed at codegen; a dispatch failure is a [*PluginError]. Discovery dispatches an
//     unmatched token to <prefix><token> the same way.
//
// A composed child is generated on its own, so its typed inputs start at its own root: its
// handlers cannot see a parent's cascading flags through them. Hand those across as a
// [Dependency]: the child's package declares it, and the parent — which imports the child,
// never the other way — collects its own inputs in CascadingPreRun and sets them. That is the
// one place the parent's inputs type describes the running command, and collecting there judges
// only the parent's own inputs, so a descendant's --help and required inputs are unaffected:
//
//	// package child
//	var Kubeconfig = rotini.NewDependency[string]("child.kubeconfig")
//
//	// package parent, in its CascadingPreRun
//	in, err := rtx.Inputs[ParentInputs]()
//	if err != nil { rtx.HaltWith(err); return }
//	rtx.SetDependency(child.Kubeconfig, in.Parent.Flags.Kubeconfig)
//
// `rotini validate` follows refs, validates each locally composed spec as its own document and
// collision-checks the assembled tree, so a duplicate name, a cycle, a missing ref or a mistake
// inside a child is caught before codegen. Generation is hermetic: rotini has no
// fetcher, a local ref reads the filesystem, a mod:// ref reads the module cache, and a git::
// or raw https:// ref is refused.
//
// # The runtime
//
// The generated entrypoint builds a [Program] with [NewProgram] and calls [Program.Execute]:
// resolve the invoked command from argv, run its [Handler] hooks, and exit. Each invocation
// carries a [Context] — the argv, the resolved chain, the program's streams, and the program's
// dependencies. Stopping is deliberate ([Context.HaltWith], [Context.Halt],
// [Context.HaltWithCode], [Context.Exit]), and a recorded error, recovered panic or detected
// fault is reported once, after teardown, through the outcome reporter.
//
// The runtime only works out which command was invoked. Flags and arguments are parsed and
// validated when a handler calls [Context.Inputs] (or the per-channel [Context.ArgvInputs],
// [Context.EnvInputs], [Context.FileInputs] and [Context.StdinInputs]). The generated handler
// stubs call it first thing. A handler that never calls it gets the raw [Context.Argv] and no
// validation — useful when you bring your own parser, and a trap if the call is deleted by
// accident.
//
// The runtime's only built-in behaviors, documented as the exceptions they are: a default
// SIGINT/SIGTERM trap (see [Program.WithoutSignalHandling] and [Program.WithSignals]), the
// hidden __complete entry the generated shell scripts call, and os.Exit as the default exit
// action (capture it with [Program.WithExit]).
//
// # Input values
//
// What a user can type is declared in the spec and parsed the same way on every channel an
// input reads — argv, a flag's environment and configuration fallbacks, and env and config
// inputs:
//
//   - Scalars parse into the generated field's type: string; bool (also boolean) as
//     true/false, yes/no, on/off, y/n, t/f or 1/0 in any case; the integers int, int8, int16,
//     int32, int64 and rune (also integer); the unsigned integers uint, uint8, uint16, uint32,
//     uint64 and byte; the floats float32 and float64 (also number); and any, which holds the
//     raw text. complex64, complex128 and uintptr have no parser and are refused.
//   - Value types parse a kind of value: duration with Go's units plus d and w (7d, 2w3d);
//     time and datetime (RFC 3339) and date (2026-09-29), each taking `layout:` for another
//     format; url, email, timezone, mac, ip, cidr and hostport; bytesize ([ByteSize]: 512Mi,
//     10MB), hexbytes ([HexBytes]) and base64bytes ([Base64Bytes]).
//   - Path checks: existingfile and existingdir are plain strings, checked when parsed to exist
//     and to be that kind of thing.
//   - Shapes change how a flag is written: count (flags only) counts occurrences (-vvv is 3);
//     a list ([]T, or array with `items:`) and a map (map[string]T, or map and object) repeat.
//   - Any other type parses through its own encoding.TextUnmarshaler, with `import:` naming
//     its package.
//   - A list or map flag repeats (--tag a --tag b, --label k=v); with `separator:` one value
//     also splits (--tag a,b), CSV-style, before validation sees the items.
//   - `implicit_value:` makes a flag's value optional: bare --color takes it, --color=never
//     sets one, and the next word is never consumed.
//   - An `enum` matches exactly, or regardless of case with `ignore_case:`, binding the
//     declared spelling.
//   - A flag whose schema is a named object ($ref: '#/schemas/DB') takes a structured value:
//     JSON (--db '{"host":"h","port":5}'), key=value pairs (--db host=h,port=5, dotted keys
//     nesting, quotes keeping a comma), a JSON or YAML file with `from: [file]` (--db @db.yaml),
//     or one field per flag (--db.host=h). Occurrences merge in order, a later key winning; a
//     list of objects takes one element per occurrence. Every spelling is validated against
//     the named schema, the one a stdin payload of that shape meets. Where the schema says
//     nothing about a value — inside a free-form map, or in a `dotted_keys:` map — key=value
//     text is read as its JSON spelling would be: true, false, null and JSON numbers are
//     typed, anything else stays text. So `-p spec.replicas=5` and `-p '{"spec":{"replicas":5}}'`
//     store the same number.
//
// # Slices at the boundary
//
// One rule, because the two directions differ and the difference has bitten:
//
//   - A slice rotini RETURNS is a copy. [Context.CommandChain] and every [Outcome] channel hand back
//     their own, so sorting, reslicing or editing one cannot reach the run. Chain used to be
//     the live slice with a doc asking callers to treat it as read-only, and a single
//     assignment through it silently rewrote [Context.CommandPath] for the rest of the invocation.
//
//   - A slice you PASS IN is kept, not copied. [Program.WithArgs], [Program.WithSignals] and
//     the slices inside an [InputSettings] are held by reference, so mutating yours afterwards
//     changes the program. Copying them defensively would cost every caller for a mistake
//     almost nobody makes; saying so costs nothing.
//
// [Context.Argv] is the deliberate exception in the first group: it is documented as the live
// argv precisely so a handler can run its own parser over it.
//
// # Outcomes
//
// A run reports through one reporter ([Program.WithReporter]), handed all five recorded channels
// at once as an [Outcome], fired once after the lifecycle settles. A handler does not print —
// it records, and the runtime reports:
//
//   - [Context.RecordInfo] — neutral informational output.
//   - [Context.RecordSuccess] — what went right.
//   - [Context.RecordWarning] — non-fatal: a deprecation, a fallback. Never changes the code.
//   - [Context.RecordError] — the end-user's own failures: a bad input, a domain error.
//   - Recovered panics and rotini-detected faults. There is no record call: the lifecycle
//     captures them, and the reporter receives them as its panics slice.
//
// Recording is non-halting: a handler records any number of times across any hook, then stops
// independently, or simply returns. The reporter fires only when some channel is non-empty, so a
// run that records nothing is a silent success.
//
// There are four ways to stop, and which one to reach for is decided by whether something
// failed and whether the exit code is the point:
//
//   - [Context.HaltWith] records an error and stops forward progress as one act, claiming no
//     code. This is the commonest stop — a hook that has failed — and the one to prefer when a
//     program centralizes its exit policy in a reporter.
//   - [Context.Halt] stops forward progress with nothing to record and claims NO code, leaving
//     the verdict to what the run recorded and to the reporter.
//   - [Context.HaltWithCode] stops AND claims a code, for when the number is the point: a filter
//     reporting "no match" as 1, a wrapper passing a child's status through.
//   - [Context.Exit] stops immediately and skips pending teardown, for when remaining cleanup
//     must not run.
//
// Halting matters as much as recording. A hook that records a failure and returns without
// stopping lets the next hook collect the same inputs, hit the same validation and record the
// same error again.
//
// The default reporter prints info → warning → error → panic → success, infos and successes to
// stdout and the rest to stderr, then applies the exit floor: a recorded error or fault exits
// 1 unless a handler already set a deliberate code, which it never downgrades. The reporter is
// the final authority, so a custom one owns the exit entirely. rotini holds no named exit-code
// constants.
//
// Every failure class is errors.Is-able against the [ErrUsage] or [ErrInternal] sentinel, so
// [CategoryOf] classifies it — except a plugin timeout, which is deliberately [CategoryNone] —
// and errors.As-able to a typed value with structured fields.
// rotini's own messages are non-leaky — no recon, decode or OS internals, and no secret values:
//
//   - [*ParseError] — the argv channel. [ParseError.Kind] branches it without matching the
//     message; Token and Candidates are what a [Suggestor] turns into "did you mean".
//   - [*InputError] — the env, config, stdin and flag-fallback channels, carrying the channel,
//     the input and a clean message, with the recon cause reachable via errors.As.
//   - [*PluginError] — a plugin dispatch, recorded as an error. A discovered plugin that is
//     missing is a usage error (the user's typo); a declared one that is missing, or a plugin
//     that cannot start, is internal (an install problem); a timeout is neither.
//   - [*DependencyError] and [*PanicError] arrive as panics, and so does a [*WiringError] from the
//     program's own wiring. The one [*WiringError] [Context.Inputs] returns — config inputs on a
//     program built without an [InputSettings] — comes back as an error instead.
//
// rotini ships no opinions on top: no "did you mean", no help dump on error. A program that
// wants either writes its own reporter.
//
// # Sharing dependencies between handlers
//
// The store, client or logger every handler needs is a dependency, named by a typed
// [Dependency] handle so the name and the type cannot drift apart:
//
//	// declared once, beside the thing it names
//	var Store = rotini.NewDependency[*store.Store]("tasks.store")
//
//	// main.go — the value's type is checked here, where it is supplied
//	cmd.Program.WithDependency(tasks.Store, store.New()).Execute()
//
//	// or, for several at once ([WithDependency] and [Program.With])
//	cmd.Program.
//		With(
//			rotini.WithDependency(tasks.Store, store.New()),
//			rotini.WithDependency(tasks.Client, client.New()),
//		).
//		WithVersion(version).
//		Execute()
//
//	// any handler — no string, no type assertion, no miss check
//	s := rtx.MustGetDependency(tasks.Store)
//
// [Context.GetDependency] reports a miss instead of routing it to the reporter, and
// [Context.SetDependency] sets one for the rest of this run only. A handler that needs to know
// which command it is asks the context: [Context.Command] is the resolved leaf,
// [Context.CommandPath] the canonical invocation ("tasks add"), and [Context.CommandChain] the
// full chain with the tokens the user actually typed.
//
// # Opt-in services
//
// Everything else is a function or type a handler calls when it wants it. None of it needs
// registering: the dependencies — [Program.WithDependency] to provide,
// [Context.GetDependency] or [Context.MustGetDependency] to consume — hold only the program's
// own.
//
// rotini's OWN seams are not dependencies. [Program.WithInputSettings], [Program.WithInputReader],
// [Program.WithParser], [Program.WithVersion] and [Program.WithHelp] supply them;
// [Context.Parser], [Context.Version] and [Context.Help] read them back. The dependencies are
// yours alone, so nothing rotini depends on can be shadowed by a name you chose or a type you
// got wrong:
//
//   - [Context.Inputs] is the typical handler's whole input story: every declared channel
//     reconciled and validated in one line, into the command's generated inputs type.
//
//     inputs, err := rtx.Inputs[DeployInputs]()
//
//     [Context.InputsWithReport] adds the provenance [InputReport]. Both ride the [InputSettings]
//     the generated NewProgram supplies via [Program.WithInputSettings].
//
//   - [Parser] parses and validates the argv channel alone — GNU/POSIX grammar, typed
//     coercion, enum and constraint checks — failing with a [*ParseError]. [InputReader] is the
//     engine behind [Context.Inputs], for callers who want to hold the meta explicitly.
//     Neither needs supplying to be used: [Context.Inputs] builds its own.
//     [Program.WithParser] replaces only the parser that [Context.Parser] returns.
//
//   - [Deprecations] reports the deprecated aliases and identifiers this invocation actually
//     used. It is a plain function over the [Context] and needs nothing registered.
//
//   - The per-channel methods ([Context.ArgvInputs], [Context.EnvInputs],
//     [Context.FileInputs], [Context.StdinInputs], [Context.DefaultInputs], merged by
//     [MergeInputs] or [MergeInputsWithReport]) acquire channels one at a time, for programs
//     that want custom precedence.
//
//   - [Suggestor] turns a [*ParseError]'s rejected token and candidate vocabulary into "did you
//     mean" suggestions — [Suggestor.For] does it in one call. Constructing one is the whole of
//     the opt-in: rotini emits nothing of its own, and what to say stays with the program.
//
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve and orchestration
//     phases wholesale; [FlagValueCompleter] and [ArgValueCompleter] feed dynamic completion,
//     and [Program.Complete] answers it in a host's [CompletionFormat] — [PluginCompletion] for
//     a plugin host such as kubectl completing a rotini plugin; [Program.WithCompletion]
//     makes __complete itself answer that way, for hosts that call it (Docker, Flux).
//
// # Batteries
//
// rotini wires nothing on import, starts no goroutine and touches no terminal. It ships no
// styler, table, spinner, prompt, pager, terminal probe or process runner: drawing to and
// reading from a terminal, and running other programs, are solved problems with better
// libraries behind them (golang.org/x/term, os/exec) than a CLI framework should be writing on
// the side. The one text helper it keeps is the one its own generated pages need:
//
//   - [StripANSI] removes ANSI escape sequences, which is what makes a styled string safe to put
//     in a man page, a markdown page or a completion description.
//
// # Program shapes
//
// A rotini binary is not always a one-shot command. An interactive loop, a daemon, or a server
// answering a peer all run the same program in a different shape, resting on [Program.Run]
// being re-entrant — each dispatch gets a fresh [Context], so nothing leaks between invocations
// while dependencies registered once up front reach all of them. Run is also safe to call
// CONCURRENTLY once configuration is done; the handlers value and the program's streams stay
// shared, so a concurrent host synchronizes those. See [Program.Run].
//
// rotini ships no loop of its own. A host calls [Program.RunContext] once per line or request;
// supplying the context hands signal handling to the host, so it decides what ^C cancels.
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
