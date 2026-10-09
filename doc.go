// Package rotini is the runtime for rotini-built command-line programs. A CLI is declared in a
// spec file, generated into Go with the rotini tool, and run on this runtime, which does nothing
// the spec did not declare; everything beyond dispatch is an explicit, opt-in service.
//
// One module serves two roles at one version:
//
//   - Tool: `go get -tool github.com/go-rotini/rotini/cmd/rotini@latest` installs the codegen
//     binary (`go tool rotini init` / `generate` / `validate`), which compiles a spec into a
//     per-CLI framework file plus one editable handler file per command.
//   - Library: `go get github.com/go-rotini/rotini` provides this package, which generated
//     code imports and handlers are written against.
//
// The companion CLI under cmd/rotini is built with rotini; docs/assets/examples/ holds a spec
// and a conf that use every key.
//
// # Declare
//
// A CLI is a .rotini.spec.yaml (or json, jsonc, toml) document declaring the command tree and
// every input channel — argv flags and positional arguments, the value sentinels (from: file's
// @path, from: stdin's -), environment variables and flags' env fallbacks, configuration files,
// a typed stdin payload, declared defaults — plus documentation, shell completion and plugin
// dispatch.
//
// `rotini validate` checks the spec: the JSON Schema rejects what it can express and lint rules
// reject the rest, each problem reported at file:line:col.
//
// # Generate
//
// `rotini generate` compiles the spec into a framework file — the [Definition] literal, typed
// per-command input structs, embedded help, man and markdown pages, and completion scripts —
// plus one handler stub per command, created once and then owned by the program. `rotini init`
// scaffolds a working CLI: a spec declaring -h/--help, -v/--version and the help and version
// commands, a conf, an entrypoint, and a stub per command wired to the generated help pages
// and the program's version. All of it may be edited or deleted.
//
// # Composition
//
// A command can be composed at any node, so a CLI is assembled from specs as its tree is
// assembled from commands. Five modes determine where a command's spec and handler code come
// from:
//
//  1. Standalone — the command's own spec node and generated stub. The default.
//  2. Inline with handler passthrough — the command's own spec node, with typed inputs
//     generated locally and handler code from a package named by
//     handler: { import: …, convention: … }. Codegen emits the delegating call and seeds no
//     stub. It does not cascade: an inline sub-command without its own handler gets a stub.
//  3. Local composition — a "$ref" to a sibling spec in the same module. The child's tree
//     merges in, the parent winning on an overlapping key, and each composed command
//     delegates to the child's generated package.
//  4. Module composition — a "$ref" to mod://<module>@<version>/<path>, resolved through the
//     Go module cache and pinned by go.sum, delegating to that module's generated package.
//  5. Declared plugin — a sibling binary <program>-<name>, dispatched at run time rather than
//     composed at codegen; a dispatch failure is a [*PluginError]. Plugin discovery dispatches
//     an unmatched token to <prefix><token> the same way.
//
// A composed child is generated on its own, so its typed inputs start at its own root and do
// not include a parent's cascading flags. The parent passes those as a [Dependency]: the child's
// package declares it, and the parent, which imports the child, collects its own inputs in
// CascadingPreRun and sets it. Collecting there validates only the parent's own inputs, so a
// descendant's --help and required inputs are unaffected:
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
// checks the assembled tree for collisions, so a duplicate name, a cycle, a missing ref or an
// error inside a child is caught before codegen. Generation is hermetic: a local ref reads the
// filesystem, a mod:// ref reads the module cache, and a git:: or raw https:// ref is refused.
//
// # The runtime
//
// The generated entrypoint builds a [Program] with [NewProgramFunc] and calls [Program.Execute],
// which resolves the invoked command from argv, runs its [Handler] hooks, and exits. Each
// invocation carries a [Context]: the argv, the resolved chain, the program's streams and its
// dependencies. A handler stops with [Context.HaltWith], [Context.Halt],
// [Context.HaltWithCode] or [Context.Exit]; recorded errors, recovered panics and detected
// faults are reported once, after teardown, by the outcome reporter.
//
// The runtime resolves only which command was invoked. Flags and arguments are parsed and
// validated when a handler calls [Context.Inputs] (or the per-channel [Context.ArgvInputs],
// [Context.EnvInputs], [Context.FileInputs] and [Context.StdinInputs]), which the generated
// stubs do first. A handler that never calls it reads the raw [Context.Argv] and gets no
// validation; [Context.CheckInputs] checks inputs it collects another way. A flag marked
// short_circuit in the spec ([FlagDef.ShortCircuit]), such as --help, waives every declared
// requirement when it is set on the command line, so the handler can act on it.
//
// A run reads the process environment and working directory unless the program gives it its
// own with [Program.WithEnviron] and [Program.WithDir]. Env inputs and fallbacks, config file
// paths and discovery, `@file` and existingfile paths, and plugins all read through them, and a
// handler reads them with [Context.LookupEnv], [Context.Environ] and [Context.Dir].
//
// The runtime's only built-in behaviors are a default SIGINT/SIGTERM trap (see
// [Program.WithoutSignalHandling] and [Program.WithSignals]), the hidden __complete entry the
// generated shell scripts call, and os.Exit as the default exit action (see [Program.WithExit]).
//
// # Input values
//
// Input types are declared in the spec and parsed the same way on every channel an input
// reads: argv, a flag's environment and configuration fallbacks, and env and config inputs.
//
//   - Scalars parse into the generated field's type: string; bool (also boolean) as
//     true/false, yes/no, on/off, y/n, t/f or 1/0 in any case; the integers int, int8, int16,
//     int32, int64 and rune (also integer); the unsigned integers uint, uint8, uint16, uint32,
//     uint64 and byte; the floats float32 and float64 (also number); and any, which holds the
//     raw text. complex64, complex128 and uintptr have no parser and are refused.
//   - Value types: duration with Go's units plus d and w (7d, 2w3d); time and datetime
//     (RFC 3339) and date (2026-09-29), each taking `layout:` for another format; url, email,
//     timezone, mac, ip, cidr and hostport; bytesize ([ByteSize]: 512Mi, 10MB), hexbytes
//     ([HexBytes]) and base64bytes ([Base64Bytes]).
//   - Path checks: existingfile and existingdir are strings checked at parse time to exist and
//     to be that kind of entry.
//   - Shapes: count (flags only) counts occurrences (-vvv is 3); a list ([]T, or array with
//     `items:`) and a map (map[string]T, or map and object) repeat.
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
//     list of objects takes one element per occurrence. Every form is validated against the
//     named schema. Where the schema does not type a value (inside a free-form map, or in a
//     `dotted_keys:` map), key=value text is read as JSON would read it: true, false, null and
//     JSON numbers are typed, anything else is text, so `-p spec.replicas=5` and
//     `-p '{"spec":{"replicas":5}}'` store the same number.
//
// # Slices at the boundary
//
//   - A slice rotini returns is a copy. [Context.CommandChain] and every [Outcome] channel
//     return their own, so modifying one does not affect the run. [Context.Argv] is the
//     exception: it is the live argv, for a handler's own parser.
//   - A slice passed in is retained, not copied. [Program.WithArgs], [Program.WithSignals] and
//     the slices inside an [InputSettings] are held by reference, so modifying the caller's
//     slice afterwards changes the program.
//
// # Outcomes
//
// A handler does not print its outcome; it records it, and one reporter ([Program.WithReporter])
// receives all five channels as an [Outcome] once the lifecycle finishes:
//
//   - [Context.RecordInfo] — neutral informational output.
//   - [Context.RecordSuccess] — what succeeded.
//   - [Context.RecordWarning] — non-fatal: a deprecation, a fallback. Never changes the code.
//   - [Context.RecordError] — the end user's failures: a bad input, a domain error.
//   - Recovered panics and rotini-detected faults, captured by the runtime; there is no record
//     call for them.
//
// Recording does not stop the run. The reporter is called only when some channel is non-empty,
// so a run that records nothing exits silently with code 0 unless a handler set one.
//
// A handler stops in one of four ways:
//
//   - [Context.HaltWith] records an error and stops forward progress, setting no code. It is
//     the usual way for a hook to fail.
//   - [Context.Halt] stops forward progress without recording anything or setting a code.
//   - [Context.HaltWithCode] stops and sets a code, when the code carries meaning: a filter
//     reporting "no match" as 1, a wrapper passing through a child's status.
//   - [Context.Exit] stops and sets a code, skipping pending teardown.
//
// A hook that records a failure without stopping lets the next hook run, often repeating the
// same failure.
//
// The default reporter prints infos, warnings, errors, panics, then successes (infos and
// successes to stdout, the rest to stderr), then applies the exit floor: a recorded error or
// fault exits 1 unless a handler already set a non-zero code. A custom reporter owns the exit
// code entirely.
//
// Every failure class is errors.Is-able against the [ErrUsage] or [ErrInternal] sentinel, so
// [CategoryOf] classifies it (except a plugin timeout, which is [CategoryNone]), and
// errors.As-able to a typed value with structured fields. Rotini's own messages expose no
// decoder or OS internals and no secret values:
//
//   - [*ParseError] — the argv channel. [ParseError.Kind] identifies the failure; Token and
//     Candidates are the facts a "did you mean" needs.
//   - [*InputError] — the env, config, stdin and flag-fallback channels, carrying the channel,
//     the input and a message, with the recon cause reachable via errors.As. An env or config
//     value outside its enum also carries Token and Candidates.
//   - [*PluginError] — a plugin dispatch, recorded as an error. A missing discovered plugin is
//     a usage error; a missing declared plugin, or a plugin that cannot start, is internal; a
//     timeout is neither. A missing discovered plugin carries Candidates: the names the
//     command knows, to rank its Name against.
//   - [*DependencyError] and [*PanicError] arrive as panics, as does a [*WiringError] from the
//     program's wiring. [Context.Inputs] returns one [*WiringError] as an error instead: config
//     inputs requested on a program built without an [InputSettings].
//
// Rotini prints no "did you mean" suggestions and no help on error. A program that wants
// either branches on the error in its handler (errors.As to the type, then its Kind or
// [CategoryOf]) or writes its own reporter. A value a flag's environment or config fallback
// supplied is named with its source in a value error, so the user can find it.
//
// # Sharing dependencies between handlers
//
// A value every handler needs (a store, a client, a logger) is a dependency, named by a typed
// [Dependency] handle so its name and type cannot drift apart:
//
//	// declared once, beside the thing it names
//	var Store = rotini.NewDependency[*store.Store]("tasks.store")
//
//	// main.go — the value's type is checked where it is supplied
//	cmd.Program.WithDependency(tasks.Store, store.New()).Execute()
//
//	// or several at once ([WithDependency] and [Program.With])
//	cmd.Program.
//		With(
//			rotini.WithDependency(tasks.Store, store.New()),
//			rotini.WithDependency(tasks.Client, client.New()),
//		).
//		WithVersion(version).
//		Execute()
//
//	// any handler
//	s := rtx.MustGetDependency(tasks.Store)
//
// [Context.GetDependency] reports a miss instead of routing it to the reporter, and
// [Context.SetDependency] sets one for the rest of this run only. [Context.Command] is the
// command whose hook is running, [Context.CommandPath] its canonical path ("tasks add"), and
// [Context.CommandChain] the full chain with the tokens the user typed.
//
// # Opt-in services
//
// Everything else is a function or type a handler calls when it needs it, with nothing to
// register. Rotini's own settings are typed options, not dependencies, so a dependency can
// never shadow one: [Program.WithInputSettings], [Program.WithInputReader], [Program.WithParser],
// [Program.WithVersion] and [Program.WithHelp] set them; [Context.Parser], [Context.Version] and
// [Context.Help] read them.
//
//   - [Context.Inputs] reads every declared channel, reconciled and validated, into the
//     command's generated inputs type:
//
//     inputs, err := rtx.Inputs[DeployInputs]()
//
//     [Context.InputsWithReport] adds the provenance [InputReport]. Both use the
//     [InputSettings] the generated NewProgram supplies via [Program.WithInputSettings].
//
//   - [Parser] parses and validates the argv channel alone (GNU/POSIX grammar, typed
//     coercion, enum and constraint checks), failing with a [*ParseError]. [InputReader] is the
//     engine behind [Context.Inputs], for callers who hold the settings explicitly. Neither needs
//     to be supplied; [Program.WithParser] replaces only the parser [Context.Parser] returns.
//
//   - [Deprecations] reports the deprecated aliases and identifiers this invocation used.
//
//   - [Context.WriteOutput] writes a command's output in the format the handler passes: json,
//     yaml and toml by rotini, any other by the handler's renderer. [Context.WriteOutputItem]
//     writes one item of a stream. [Context.CheckOutput], [Program.WithOutputChecks] and
//     [DecodeOutput] check values against the declared shape, and [StructuredReporter] reports
//     a run's outcome as JSON lines on stderr when the program's rule marks the run structured.
//
//   - The per-channel methods ([Context.ArgvInputs], [Context.EnvInputs],
//     [Context.FileInputs], [Context.StdinInputs], [Context.DefaultInputs], merged by
//     [MergeInputs] or [MergeInputsWithReport]) read channels one at a time, for programs
//     with custom precedence.
//
//   - [Context.CheckInputs] checks inputs the program collected itself (a prompt, a secrets
//     service, a test) against the spec, with [PresenceOf] marking which fields were supplied;
//     a hand-built [InputLayer] merged with rotini's is checked the same way by
//     [InputReport.Validate].
//
//   - [SuggestionFacts] reads the rejected token and its candidates from any error that carries
//     them, and [Suggestor] ranks them into "did you mean" suggestions; [Suggestor.For] does
//     both in one call.
//
//   - [Program.WithResolver] and [Program.WithLifecycle] replace the resolve and run phases.
//     [FlagValueCompleter] and [ArgValueCompleter] supply dynamic completion, and
//     [Program.Complete] answers it in a host's [CompletionFormat]: [PluginCompletion] for a
//     plugin host such as kubectl completing a rotini plugin. [Program.WithCompletion] makes
//     __complete answer in that format, for hosts that call it (Docker, Flux). When the conf
//     turns completion messages on, a completer adds its own with
//     [Context.AddCompletionMessage], and [Program.WithCompletionMessages] decides when they
//     show.
//
// # Batteries
//
// Rotini does nothing on import, starts no background goroutine and touches no terminal. It
// ships no styler, table, spinner, prompt, pager, terminal probe or process runner; use
// golang.org/x/term, os/exec and similar libraries. Its one text helper:
//
//   - [StripANSI] removes ANSI escape sequences, making a styled string safe for a man page, a
//     markdown page or a completion description.
//
// # Program shapes
//
// An interactive loop, a daemon or a server answering a peer runs the same program by calling
// [Program.Run] repeatedly. Each dispatch gets a fresh [Context], so nothing carries over
// between invocations, while dependencies registered up front reach all of them. Run is safe for
// concurrent use once configuration is complete; the handlers value and the program's streams
// remain shared, so a concurrent host synchronizes those. See [Program.Run].
//
// Rotini ships no loop. A host calls [Program.RunContext] once per line or request; supplying
// the context leaves signal handling to the host.
//
// # What rotini does not ship
//
// Some functionality lives in sibling modules, imported directly:
//
//   - watching files — go-rotini/fs, fs.NewWatcher
//   - a single-instance lock — go-rotini/fs, fs.PIDLock
//   - caching in a long-running program — go-rotini/memcache
package rotini
