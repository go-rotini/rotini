package rotini

import (
	"bytes"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

// Context is rotini's per-invocation context: the program's dependencies, its streams, and what
// the runtime resolved before dispatch — the raw argument vector ([Context.Argv]) and the
// resolved command chain ([Context.CommandChain]). One is built per [Program.Run] and passed to
// every hook, so all hooks share the same bindings and exit state and no records leak between
// invocations.
//
// The surface groups into seven jobs, and nothing outside them is worth hunting for:
//
//   - what was typed — the [Context.Argv], [Context.Stdin], [Context.Stdout] and
//     [Context.Stderr] fields above
//   - which command — [Context.Command] is the command whose hook is running (its Invoked field
//     says whether it is the one the user ran), [Context.CommandPath] names it, and
//     [Context.CommandChain] is every command from the root to the invoked one
//   - inputs — [Context.Inputs] for the command's validated inputs in one call,
//     [Context.InputsWithReport] for the same plus where each value came from, and the
//     per-channel [Context.ArgvInputs], [Context.EnvInputs], [Context.FileInputs],
//     [Context.StdinInputs] and [Context.DefaultInputs]
//   - YOUR dependencies — [Context.GetDependency] and [Context.MustGetDependency] to read one,
//     [Context.SetDependency] and [Context.SetDependencyIfAbsent] to set one for this run
//   - report what happened — [Context.RecordInfo], [Context.RecordSuccess],
//     [Context.RecordWarning], [Context.RecordError], and [Context.Failed] to ask
//   - stop — [Context.HaltWith] to fail, [Context.Halt] to stop, [Context.HaltWithCode] when
//     the code is the point, [Context.Exit] to skip pending teardown
//   - rotini's own seams — [Context.Version], [Context.Help] and [Context.Parser] to read, and
//     for a Context you built yourself rather than one the runtime handed you,
//     [Context.WithVersion], [Context.WithHelp], [Context.WithParser], [Context.WithInputSettings]
//     and [Context.WithInputReader] to set
//
// Parsing is opt-in: nothing is parsed or validated until a handler calls one of the inputs
// methods. A CLI that wants raw argv never calls them and reads [Context.Argv].
//
// Dependencies are the dependency-injection seam: a typed [Dependency] handle names each one.
// Those registered with [Program.WithDependency] are seeded into every run; one set with
// [Context.SetDependency] lasts the lifetime of this Context.
//
// A Context is safe for concurrent access — a handler may read it, record outcomes
// and reach its seams from goroutines it spawned. Always pass it as a pointer; it must not be
// copied.
//
// Safe is not the same as unchanging. [Context.Command] tracks the lifecycle's progress, so a
// goroutine that outlives the hook that spawned it reads the step running when it looks rather
// than the step that started it — see [Context.Command]. Everything else a handler reads here
// is fixed for the run.
//
// A nil *Context is a caller bug, not a state to handle: the runtime always hands a real one
// to every hook, and [NewContextFor] never returns nil, so the methods a handler reads and
// records through dereference rather than check. That is the same rule [Program] follows, and
// for the same reason: a nil that reports "nothing recorded" or "no chain" hides the mistake
// and surfaces it somewhere later, at a call that was not wrong. The standalone setters
// ([Context.WithVersion] and its siblings) are the exception — on a nil Context they do nothing
// and return nil.
//
// rotini's own input entry points still check it: the inputs methods ([Context.Inputs] and its
// per-channel siblings) report a nil Context as an error, and [Deprecations], which accepts a
// Context from a caller, reports none. The guard belongs at the boundary, not on every method
// behind it.
type Context struct {
	mu sync.RWMutex

	// Stdin, Stdout and Stderr are the program's streams, mirroring [Program.WithStdin] and
	// friends. A handler reads and writes through these rather than os.Std* directly, so the
	// same handler code can be driven by a test that configures the Program's streams. They
	// are set before dispatch, never mutated by rotini thereafter, and never nil.
	//
	// They are for READING. Assigning one is not supported and does not do what it looks like:
	// the default reporter reports through the PROGRAM's streams, so a hook that swaps
	// rtx.Stdout redirects its own writes and nothing else — the run's errors still go where
	// they were always going. To redirect a whole invocation, configure the Program
	// ([Program.WithStdout]) or give the run its own ([Program.RunContext] on a Program built
	// for it).
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Argv is the raw argument vector for this invocation, with everything after the resolved
	// command path still present, so a handler can run its own parser instead of
	// [Parser.Parse]. It is the live slice, not a copy: a handler that mutates it changes what
	// every later read sees, including the Parser and InputReader.
	//
	// Argv, not Args: these are the invocation's raw tokens, command names and flags included.
	// A command's DECLARED positionals are the generated inputs' Arguments field, already
	// parsed, typed and validated — a different thing that a handler reaches for far more often.
	Argv []string

	services      map[string]any
	chain         []Command // resolved command path, root → leaf
	exitCode      int       // first non-zero wins; the reporter overrides
	stopped       bool      // an exit was requested: forward progress halts
	exitNow       bool      // hard Exit: skip remaining teardown too
	reporterStage bool      // the reporter is executing: Exit overrides, HaltWithCode is a no-op
	infos         []string  // the five outcome channels, all private: they surface
	errs          []error   // only as the slices handed to the reporter. faults are
	warnings      []error   // the lifecycle's to capture, never a handler's to record.
	successes     []string
	faults        []*PanicError

	// frame is the chain index of the command whose hook is currently running, or -1 for
	// "not inside a hook", which resolves to the leaf. The lifecycle sets it around every
	// step (see [AsCommand]); it is what lets an inputs struct be anchored on the caller's own
	// command rather than guessed from its field count. See [Context.Command].
	frame int

	// rotini's own seams, seeded from the Program each run — see [Program.WithInputSettings].
	// They are deliberately NOT in services: the registry is the user's namespace, and a
	// value the runtime depends on must not share a flat keyspace with it.
	meta     *InputSettings
	readerFn func(InputSettings) *InputReader
	version  string
	help     HelpFunc
	parser   *Parser

	// flagStdinMemo is stdin as the flag channel saw it: read once, the first time a `from: [stdin]`
	// flag's "-" asks for it, and replayed to every later parse of the same run. Parsing argv
	// resolves that sentinel, and argv is parsed more than once in a run — the generated --help
	// check, a parent collecting its own inputs, Inputs itself — while stdin can be read once.
	flagStdinMemo *stdinMemo
}

// stdinMemo reads a stream to EOF once and replays it.
type stdinMemo struct {
	once sync.Once
	src  io.Reader
	data []byte
	err  error
}

// flagStdin is the reader a parse resolves `from: [stdin]` against: each call returns a fresh
// replay of one shared, lazily performed read of rtx.Stdin, so a second parse sees what the first
// consumed. Nothing is read unless a "-" value actually asks.
func (rtx *Context) flagStdin() io.Reader {
	if rtx.Stdin == nil {
		return nil
	}
	if rtx.flagStdinMemo == nil || rtx.flagStdinMemo.src != rtx.Stdin {
		rtx.flagStdinMemo = &stdinMemo{src: rtx.Stdin}
	}
	return &memoReader{m: rtx.flagStdinMemo}
}

type memoReader struct {
	m *stdinMemo
	r io.Reader // this replay's position in m.data, once the read has happened
}

func (r *memoReader) Read(p []byte) (int, error) {
	if r.r == nil {
		// readStdin, not io.ReadAll: an interactive terminal is "nothing piped", not a read that
		// blocks on the keyboard.
		r.m.once.Do(func() { r.m.data, r.m.err = readStdin(r.m.src) })
		if r.m.err != nil {
			return 0, r.m.err
		}
		r.r = bytes.NewReader(r.m.data)
	}
	return r.r.Read(p)
}

// cloneServices snapshots the registry's bindings. Every run's Context is seeded from the
// Program's registry through it, so a service bound before the run is visible to every run
// while one bound during a run stays local to it.
func (rtx *Context) cloneServices() map[string]any {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return maps.Clone(rtx.services)
}

// newContext returns an empty [Context] with an initialized registry and no resolved command.
// [NewContextFor] is the public entry.
func newContext() *Context {
	return &Context{
		services: make(map[string]any),
		frame:    frameUnset,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
}

// NewContextFor builds a [Context] with argv resolved against an explicit def — the same
// context the runtime hands a handler at dispatch. Use it to exercise the [Parser] or the
// [Context.Inputs] family, or a single hook, against a Definition you construct:
//
//	def := rotini.Definition{Name: "app", Handler: "App", Commands: []rotini.CommandDef{ … }}
//	rtx := rotini.NewContextFor(def, []string{"build", "x.yaml"})
//	var in appInputs
//	err := rtx.Parser().Parse(rtx, &in)
//
// To drive a whole generated program end to end — the usual handler test — construct it with
// the generated NewProgram and run it under a recording exit and captured streams instead; the
// generated command tree is unexported.
//
// A plugin token resolves to as much of the chain as precedes it. NewContextFor does not exec
// the sibling binary the runtime would.
func NewContextFor(def Definition, argv []string) *Context {
	rtx := newContext()
	chain, _ := resolveChain(def, argv)
	markInvoked(chain)
	rtx.Argv = argv
	rtx.chain = chain
	return rtx
}

// CommandChain returns every command of this invocation, from the root to the one the user
// invoked: chain[0] is the root, and the last entry — the only one whose Invoked is set — is
// the invoked command. The [Parser] and [InputReader] read it to bind inputs against the
// running command.
//
// The slice is a COPY, so reordering, reslicing or replacing an entry is a caller's own
// business and cannot reach the run — [Context.CommandPath], [Context.Command], the input reader's
// alignment and configuration-file scoping all read the run's own chain. The outcome channels
// are copied for the same reason.
//
// The copy is one level deep, which is the boundary that exists to defend. A command's Flags,
// Arguments and Commands are the [Definition]'s own slices, shared program-wide and read-only
// across every run by the same convention that lets one Program serve a REPL; CommandChain
// neither widens nor narrows that.
func (rtx *Context) CommandChain() []Command {
	return slices.Clone(rtx.chain)
}

// markInvoked sets Invoked on the last command of chain and clears it everywhere else, so
// exactly one entry is the invoked command whoever built the chain — the default resolver, a
// replacement from [Program.WithResolver], or [NewContextFor].
func markInvoked(chain []Command) {
	for i := range chain {
		chain[i].Invoked = i == len(chain)-1
	}
}

// frameUnset marks a Context that is not inside a lifecycle step. It resolves to the invoked
// command, which is what every non-cascading hook wants.
const frameUnset = -1

// Command returns the command whose hook is running.
//
// In PreRun, Run and PostRun that is the command the user invoked. A cascading hook runs for
// every command in the chain, and there it is the command the hook belongs to:
//
//	$ mig db status
//
//	hook                                 Command()     Command().Invoked
//	────                                 ─────────     ─────────────────
//	mig's CascadingPreRun                mig           false
//	db's CascadingPreRun                 db            false
//	the leaf's PreRun / Run / PostRun    status        true
//	db's CascadingPostRun                db            false
//	mig's CascadingPostRun               mig           false
//
// Invoked is how a cascading hook tells the command the user ran from an ancestor of it:
//
//	func (*songsHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
//	    if rtx.Command().Invoked {
//	        // `musak songs` — this command IS the invocation; print help rather than defer.
//	        return
//	    }
//	    // `musak songs list` — a sub-command is running; set up for it.
//	}
//
// The rest of the chain is [Context.CommandChain]: its first entry is the root and its last is
// the invoked command. [Context.Inputs], [Context.CommandPath] and [Context.Help] all describe
// the command Command returns, which is what makes them correct in every hook — including a
// composed child's cascading hook reading its own flags.
//
// Outside a lifecycle step — a Context from [NewContextFor], or one reaching a reporter after the
// run has settled — there is no hook, and Command reports the invoked command.
//
// # It describes the step running NOW, not the one that spawned you
//
// The running step moves as the lifecycle advances, so Command answers for whichever step is
// running when it is called — not for the hook that happens to be on the stack. A goroutine a
// hook spawns and does not wait for therefore reads whatever step the run has reached by the
// time it looks:
//
//	func (*h) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
//	    go func() {
//	        // The run has moved on. This may report the invoked command, not this one.
//	        log.Println(rtx.Command().Name)
//	    }()
//	}
//
// It is not a data race — the step is mutex-guarded and every read is consistent — but the
// ANSWER is timing-dependent, and [Context.Inputs] anchors on it, so a goroutine collecting
// inputs may anchor somewhere its spawning hook did not intend. Capture what you need before
// spawning:
//
//	cmd := rtx.Command()                       // or collect the inputs here
//	go func() { log.Println(cmd.Name) }()
//
// A goroutine the hook WAITS for, before returning, sees its spawner's command.
func (rtx *Context) Command() Command {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.chain) == 0 {
		return Command{}
	}
	return rtx.chain[rtx.frameIndexLocked()]
}

// frameIndex is the chain index of the command Command reports. Callers must not hold the lock.
func (rtx *Context) frameIndex() int {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.frameIndexLocked()
}

// frameIndexLocked resolves the unset sentinel to the invoked command. The caller holds the
// lock.
func (rtx *Context) frameIndexLocked() int {
	if rtx.frame < 0 || rtx.frame >= len(rtx.chain) {
		return max(len(rtx.chain)-1, 0)
	}
	return rtx.frame
}

// setFrame records which command's hook is running, returning the previous value so
// [AsCommand] can restore it. Unexported: a handler never sets its own identity.
func (rtx *Context) setFrame(i int) int {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	prev := rtx.frame
	rtx.frame = i
	return prev
}

// CommandPath returns the path of [Context.Command], space-joined from the root — "tasks add"
// for a sub-command, "tasks" for the root. In a cascading hook running for `tasks add`, the root's
// hook sees "tasks"; the invoked command's path is the names of [Context.CommandChain].
//
// The names are canonical, not the tokens the user typed, so an invocation through an alias
// reports the real command name and a path is stable to log and aggregate on. Each command's
// Matched token in [Context.CommandChain] is what the user actually typed.
//
// CommandPath, not Path: in this API "path" already means a filesystem location
// ([Command.PluginBinary], a command's PluginPath) and a route through an inputs struct
// ([FieldPath]). This one is neither.
func (rtx *Context) CommandPath() string {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.chain) == 0 {
		return ""
	}
	names := make([]string, 0, len(rtx.chain))
	for _, c := range rtx.chain[:rtx.frameIndexLocked()+1] {
		names = append(names, c.Name)
	}
	return strings.Join(names, " ")
}

// Halt stops the lifecycle's FORWARD progress without claiming an exit code, leaving the
// verdict to whatever else the run records and to the reporter. Teardown is unaffected: every
// PostRun and CascadingPostRun whose paired setup hook began still runs, in reverse.
//
// Forward progress is the operative word, and it makes Halt load-bearing in two of the five
// hooks and a no-op in the other three:
//
//	Hook                What Halt does there
//	────                ────────────────────
//	CascadingPreRun     stops the run: no further frame's setup, no PreRun, no work
//	PreRun              stops the run: the leaf's Run never happens
//	Run                 NOTHING — this is the last forward step of the default plan
//	PostRun             NOTHING — the unwind runs to completion; only Exit cuts it short
//	CascadingPostRun    NOTHING — likewise
//
// So Halt is how a SETUP hook refuses to let the command proceed. It is safe to call anywhere
// and its own failure mode is omission, not misuse — which is why [Context.HaltWith] exists:
// it records an error and halts as one act, is correct in all five hooks, and cannot be
// half-forgotten the way `RecordError` followed by a `Halt` that is never written can be.
//
// Prefer Halt on its own when there is nothing to record — a deliberate, unremarkable stop:
//
//	if !inputs.Force && !confirmed {
//	    rtx.Halt()          // nothing failed; there is simply nothing more to do
//	    return
//	}
//
// [Context.HaltWithCode] does two jobs at once — claim the code AND stop — so a program that
// centralizes its exit policy in a reporter would have to write a number it did not mean purely
// to stop, and explain in a comment that the number was a lie. Worse, the number then reads as
// redundant: deleting it looks like tidying and silently removes the halt, so the next hook
// collects the same inputs, hits the same validation and records the same error again. That
// is not hypothetical — it is how one bad flag came to be reported three times, with a
// fourth misleading error on top, while this example was being written.
//
// Halt claims nothing, so it cannot be mistaken for policy and cannot be deleted as
// redundant. Reach for [Context.HaltWith] to fail, [Context.HaltWithCode] when the code IS the
// point, and [Context.Exit] when pending teardown must not run.
//
// Like HaltWithCode it is a no-op inside the reporter, where the lifecycle has already run.
func (rtx *Context) Halt() {
	if rtx.reporterStage {
		return
	}
	rtx.stopped = true
}

// HaltWith records err and stops the lifecycle's forward progress — [Context.RecordError] and
// [Context.Halt] as one act. It claims no exit code: the reporter decides what the failure costs.
//
//	if err := store.Save(task); err != nil {
//	    rtx.HaltWith(err)
//	    return
//	}
//
// This is the spelling to reach for when a hook has failed, and the reason it exists is that
// the two-part version can be half-written. Failing used to be "record, then stop", and the
// stop is the half that decides anything — in a setup hook, omitting it lets the command do
// the work it just established it must not do. The exit code and stderr are IDENTICAL either
// way, so nothing in the output says the work ran, and no test that asserts on output catches
// it. A single call cannot be half-forgotten.
//
// HaltWith is correct in all five hooks. Where Halt is a no-op (see its table) HaltWith
// degrades to recording alone, which is what a failure in Run or a teardown hook wants anyway
// — so a handler never has to know which hook it is in to fail correctly.
//
// A nil err records nothing and still halts, so a caller need not guard.
//
// To record a problem and CONTINUE — collecting several before anything stops, or leaving the
// decision to a later hook that gates on [Context.Failed] — use RecordError on its own. That
// remains a supported choice; HaltWith exists so it is a deliberate one rather than what
// omission gives you.
//
// Inside the reporter it does nothing at all, and does not report that it did nothing. The halt
// is a no-op there, as [Context.Halt]'s is, and the recorded error is dropped: the [Outcome] was
// snapshotted before the reporter was called, so nothing re-reads the channels afterwards. A
// reporter that fails while reporting should write to rtx.Stderr and set a code with
// [Context.Exit] — it is the final authority by then, and recording has no one left to tell.
func (rtx *Context) HaltWith(err error) {
	rtx.RecordError(err)
	rtx.Halt()
}

// HaltWithCode claims the program's exit code and stops the lifecycle's forward progress — for
// when the NUMBER is the point: a filter reporting "no match" as 1, a wrapper passing a child's
// status through. It records no error; it is a deliberate verdict, not a failure.
//
// Teardown is unaffected: every PostRun and CascadingPostRun whose paired setup hook began still
// runs, in reverse. The first non-zero code wins, so a later HaltWithCode cannot overrule an
// earlier one.
//
// It is one of the Halt family, and the family is the thing to learn:
//
//	Halt()              stop
//	HaltWith(err)       stop, and record err          — the way a hook fails
//	HaltWithCode(n)     stop, and claim exit code n    — the way a hook renders a verdict
//	Exit(n)             stop, claim n, and SKIP pending teardown
//
// Everything named Halt* leaves teardown intact. [Context.Exit] is the one that does not, which
// is the whole distinction and the reason it is spelled like [os.Exit], whose deferred functions
// do not run either.
//
// It is a no-op inside the reporter, where the lifecycle has already run; [Context.Exit] is how
// the reporter sets the code.
func (rtx *Context) HaltWithCode(code int) {
	if rtx.reporterStage {
		return
	}
	rtx.stopped = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// Exit records the program's exit code and stops the lifecycle immediately — every pending
// teardown hook is skipped. Use it where remaining cleanup must not run; prefer
// [Context.HaltWithCode] for an orderly stop. The first non-zero code wins, and Exit is a
// deliberate stop, not an error.
//
// It skips teardown, not fault reporting: a panic recovered before Exit still reaches the
// reporter, so a handler cannot silently swallow one — though the reporter, as the final
// authority, may.
//
// Inside the reporter, Exit overrides any code the lifecycle set; during the lifecycle it keeps
// first-non-zero-wins.
func (rtx *Context) Exit(code int) {
	rtx.stopped = true
	rtx.exitNow = true
	if rtx.reporterStage {
		rtx.exitCode = code // the reporter is the final authority — override any prior code
		return
	}
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// RecordInfo records msg as an informational message of this run — neutral output such as
// progress or context, distinct from a success message only by intent. Like every record call
// it neither prints nor stops the lifecycle: the reporter receives the infos once the run
// settles. An empty msg is ignored.
func (rtx *Context) RecordInfo(msg string) {
	if msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.infos = append(rtx.infos, msg)
}

// copyInfos snapshots the recorded infos, in recording order.
func (rtx *Context) copyInfos() []string {
	return snapshot(rtx, func(c *Context) []string { return c.infos })
}

// RecordError records err as one of this run's errors — the end-user's own failures. It
// neither prints nor stops the lifecycle: a handler accumulates errors across any number of
// calls and hooks, then chooses how to stop, and the reporter receives them once the run
// settles either way. A nil err is ignored.
//
// Recovered panics and rotini-detected faults are not recorded here; the lifecycle captures
// them as the reporter's panics slice.
//
// Use it on its own when the run should CONTINUE — to collect several problems before anything
// stops, or to leave the decision to a later hook that gates on [Context.Failed]:
//
//	for _, path := range inputs.Check.Arguments.Paths {
//	    if err := validate(path); err != nil {
//	        rtx.RecordError(err) // report them all, not just the first
//	    }
//	}
//
// To fail and stop in one act, use [Context.HaltWith]. Pairing RecordError with a separate
// [Context.Halt] does the same thing, and is the form whose second half can go missing.
func (rtx *Context) RecordError(err error) {
	if err == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.errs = append(rtx.errs, err)
}

// copyErrors snapshots the recorded errors, in recording order.
func (rtx *Context) copyErrors() []error {
	return snapshot(rtx, func(c *Context) []error { return c.errs })
}

// RecordWarning records warn as a non-fatal warning of this run — a deprecation, a fallback,
// a skipped item. It is an error value so it can be typed and branched on with errors.As and
// so secrets stay redacted, but it never raises the exit code. A nil warn is ignored.
//
// Why the Record family splits its parameter type, since the names do not say: the two
// SEVERITY-bearing channels take an error, because a warning or a failure is something a reporter
// may want to branch on — categorize it with [CategoryOf], match it with errors.As, redact it.
// [Context.RecordInfo] and [Context.RecordSuccess] take a string, because neither carries
// severity and there is nothing to inspect. The asymmetry is deliberate, and the compiler tells
// you which one you are in.
func (rtx *Context) RecordWarning(warn error) {
	if warn == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.warnings = append(rtx.warnings, warn)
}

// RecordSuccess records msg as a success message of this run, for the reporter to present. An
// empty msg is ignored.
//
// The reporter reports after the lifecycle settles, so recorded outcomes appear after anything a
// handler wrote directly to [Context.Stdout] during Run.
func (rtx *Context) RecordSuccess(msg string) {
	if msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.successes = append(rtx.successes, msg)
}

// copyWarnings snapshots the recorded warnings, in recording order.
func (rtx *Context) copyWarnings() []error {
	return snapshot(rtx, func(c *Context) []error { return c.warnings })
}

// copySuccesses snapshots the recorded successes, in recording order.
func (rtx *Context) copySuccesses() []string {
	return snapshot(rtx, func(c *Context) []string { return c.successes })
}

// copyFaults snapshots the captured faults, in capture order.
func (rtx *Context) copyFaults() []*PanicError {
	return snapshot(rtx, func(c *Context) []*PanicError { return c.faults })
}

// snapshot copies one outcome channel under the read lock, returning nil for an empty channel
// or a nil Context.
func snapshot[T any](rtx *Context, channel func(*Context) []T) []T {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if s := channel(rtx); len(s) > 0 {
		return slices.Clone(s)
	}
	return nil
}

// Failed reports whether this invocation has recorded an error or suffered a fault SO FAR.
//
// It exists for teardown. A PostRun or CascadingPostRun that owns a resource has exactly one
// decision to make — commit or roll back, keep or discard, publish or delete — and it cannot
// make it without knowing whether the work it was bracketing succeeded:
//
//	func (*migrateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
//	    if rtx.Failed() {
//	        tx.Rollback()
//	        return
//	    }
//	    tx.Commit()
//	}
//
// The [Outcome] a reporter receives answers the same question, but a reporter runs AFTER every
// teardown has finished — the right place to report a failure and much too late to undo one.
//
// It is deliberately one bit and not the errors themselves. A teardown that could read them
// would be tempted to print them, and the whole point of the reporter is that a run reports its
// outcome exactly once, in one place, after everything has settled. Faults count: a panic in
// the bracketed work is a failure, and a rollback is even more clearly right there.
//
// Read from a forward hook it is also meaningful — an earlier hook in the chain may already
// have recorded an error — but the answer only grows over a run, so a false is never a promise
// about what comes next.
func (rtx *Context) Failed() bool {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return len(rtx.errs) > 0 || len(rtx.faults) > 0
}

// recordFault appends a recovered panic or detected fault. Unexported on purpose: faults are
// the lifecycle's to capture, never a handler's to record.
func (rtx *Context) recordFault(pe *PanicError) {
	if rtx == nil || pe == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.faults = append(rtx.faults, pe)
}

// ── rotini's own seams, as a standalone Context configures them ─────────────.
//
// A dispatched Context is seeded from the Program, so these are for a Context built by
// [NewContextFor] — exercising a [Parser], or driving one hook — where there is no Program to
// carry them. They mirror the [Program] options exactly, so there is one vocabulary to learn.
//
// A handler should not need any of them, and each says so, because a grouping comment in the
// source is not what a reader sees: `go doc Context.WithParser` prints that method's own lines
// and nothing else. The five sit in the same autocomplete list as [Context.Stdout], which is the
// cost of sharing one vocabulary with the Program, so each one carries the scope itself.

// WithInputSettings supplies the generated descriptor [Context.Inputs] reconciles from. See
// [Program.WithInputSettings].
//
// For a Context you built yourself. One handed to a hook is already seeded from the Program,
// and this is not scoped to the current hook: every later hook of THIS run sees the change. It
// does not outlive the run — the next invocation is seeded from the Program again.
func (rtx *Context) WithInputSettings(meta InputSettings) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.meta = &meta
		rtx.mu.Unlock()
	}
	return rtx
}

// WithInputReader replaces the input reader [Context.Inputs] uses, built from the settings. See
// [Program.WithInputReader].
//
// For a Context you built yourself. One handed to a hook is already seeded from the Program,
// and this is not scoped to the current hook: every later hook of THIS run sees the change. It
// does not outlive the run — the next invocation is seeded from the Program again.
func (rtx *Context) WithInputReader(fn func(InputSettings) *InputReader) *Context {
	if rtx != nil && fn != nil {
		rtx.mu.Lock()
		rtx.readerFn = fn
		rtx.mu.Unlock()
	}
	return rtx
}

// WithVersion sets what [Context.Version] reports. See [Program.WithVersion].
//
// For a Context you built yourself. One handed to a hook is already seeded from the Program,
// and this is not scoped to the current hook: every later hook of THIS run sees the change. It
// does not outlive the run — the next invocation is seeded from the Program again.
func (rtx *Context) WithVersion(version string) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.version = version
		rtx.mu.Unlock()
	}
	return rtx
}

// WithHelp sets where [Context.Help] finds pages. See [Program.WithHelp].
//
// For a Context you built yourself. One handed to a hook is already seeded from the Program,
// and this is not scoped to the current hook: every later hook of THIS run sees the change. It
// does not outlive the run — the next invocation is seeded from the Program again.
func (rtx *Context) WithHelp(help HelpFunc) *Context {
	if rtx != nil && help != nil {
		rtx.mu.Lock()
		rtx.help = help
		rtx.mu.Unlock()
	}
	return rtx
}

// WithParser sets the parser [Context.Parser] returns. See [Program.WithParser].
//
// For a Context you built yourself. One handed to a hook is already seeded from the Program,
// and this is not scoped to the current hook: every later hook of THIS run sees the change. It
// does not outlive the run — the next invocation is seeded from the Program again.
func (rtx *Context) WithParser(parser *Parser) *Context {
	if rtx != nil && parser != nil {
		rtx.mu.Lock()
		rtx.parser = parser
		rtx.mu.Unlock()
	}
	return rtx
}

// ── rotini's own seams, as the handler sees them ────────────────────────────.

// Help is the help page of [Context.Command] — the command whose hook is running — from
// [Program.WithHelp]: what a generated `--help` prints. It is "" when the program has no pages
// or none for this command. In PreRun, Run and PostRun that is the invoked command's page; a
// cascading hook gets its own command's page.
//
// The page is the RUNNING program's, not the one the handler was generated with. A command
// composed from another spec therefore shows its full path under the parent and the flags the
// parent passes down, the same page `help <command>` shows.
func (rtx *Context) Help() string {
	rtx.mu.RLock()
	help := rtx.help
	rtx.mu.RUnlock()
	if help == nil {
		return ""
	}
	chain := rtx.CommandChain()
	path := make([]string, 0, len(chain))
	for i := 1; i <= rtx.frameIndex() && i < len(chain); i++ {
		path = append(path, chain[i].Name)
	}
	page, err := help(path...)
	if err != nil {
		return ""
	}
	return page
}

// Version is what the program reports as its version, from [Program.WithVersion]. It is "" if
// the entrypoint set none, which is the honest answer rather than a guess.
func (rtx *Context) Version() string {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.version
}

// Parser is the [Parser] for this run: the one [Program.WithParser] supplied, or the default.
//
// It never returns nil. Parsing is not optional — [Context.Inputs] uses a parser whether or not the
// entrypoint supplied one — so a handler that wants to parse argv itself should not have to
// ask whether one exists, nor bind one to make the answer yes.
func (rtx *Context) Parser() *Parser {
	rtx.mu.RLock()
	p := rtx.parser
	rtx.mu.RUnlock()
	if p == nil {
		return NewParser()
	}
	return p
}

// bindMeta is the generated descriptor for this run, and whether the program supplied one.
func (rtx *Context) bindMeta() (InputSettings, bool) {
	if rtx == nil {
		return InputSettings{}, false
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if rtx.meta == nil {
		return InputSettings{}, false
	}
	return *rtx.meta, true
}
