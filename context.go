package rotini

import (
	"context"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

// Context is rotini's per-invocation context: the program's dependencies and streams, the raw
// argument vector ([Context.Argv]) and the resolved command chain ([Context.CommandChain]).
// One is built per [Program.Run] and passed to every hook of that run, so hooks share its
// dependencies, records and exit state, and nothing carries over between runs.
//
// The surface groups into eight jobs:
//
//   - invocation — the [Context.Argv], [Context.Stdin], [Context.Stdout] and [Context.Stderr]
//     fields, and the run's environment and working directory: [Context.LookupEnv],
//     [Context.Environ] and [Context.Dir]; [Context.DashIndex] says where a "--" was typed;
//     [Context.Context] is the running hook's context, and [Context.SetContext] hands a
//     derived one to the hooks after it
//   - command — [Context.Command] is the command whose hook is running (its Invoked field
//     reports whether the user ran it), [Context.CommandPath] names it, and
//     [Context.CommandChain] lists every command from the root to the invoked one
//   - inputs — [Context.Inputs] returns the command's validated inputs,
//     [Context.InputsWithReport] adds where each value came from, and the per-channel
//     [Context.ArgvInputs], [Context.EnvInputs], [Context.FileInputs],
//     [Context.StdinInputs] and [Context.DefaultInputs] read one channel each;
//     [Context.CheckInputs] checks inputs the program collected itself against the spec
//   - output — [Context.WriteOutput] writes the command's declared output to stdout,
//     [Context.WriteOutputItem] one item of a stream, and [Context.CheckOutput] checks a value
//     against the declared shape without writing it
//   - dependencies — [Context.GetDependency] and [Context.MustGetDependency] read one;
//     [Context.SetDependency] and [Context.SetDependencyIfAbsent] set one for this run
//   - records — [Context.RecordInfo], [Context.RecordSuccess], [Context.RecordWarning],
//     [Context.RecordError], and [Context.Failed]; during completion,
//     [Context.AddCompletionMessage] and [Context.SetCompletionOptions], [Context.PartialInputs] reads
//     what has been typed so far, and [Context.Context] is the program's base context
//   - stopping — [Context.HaltWith] to fail, [Context.Halt] to stop, [Context.HaltWithCode] to
//     stop with a code, [Context.Exit] to stop and skip pending teardown
//   - rotini's own settings — [Context.Version], [Context.Help] and [Context.Parser] read them;
//     [Context.WithVersion], [Context.WithHelp], [Context.WithParser],
//     [Context.WithInputSettings], [Context.WithInputReader], [Context.WithEnviron],
//     [Context.WithDir], [Context.WithStdin], [Context.WithStdout], [Context.WithStderr] and
//     [Context.WithOutputChecks] set them on a standalone Context
//
// Nothing is parsed or validated until a handler calls an inputs method; a handler with its
// own parser reads [Context.Argv] instead.
//
// A Context is safe for concurrent use by the goroutines a hook starts. [Context.Command]
// reflects the step running when it is called (see its doc). A Context must not be copied.
//
// Methods do not check for a nil receiver, with these exceptions: the With setters do nothing
// and return nil, the inputs methods return an error, and [Deprecations] returns none.
//
// A nil argument to a With setter restores that setting's default.
type Context struct {
	mu sync.RWMutex

	// Stdin, Stdout and Stderr are the program's streams ([Program.WithStdin] and siblings).
	// Handlers use them instead of os.Std* so tests can substitute streams. They are set before
	// dispatch and never nil.
	//
	// Set them with [Context.WithStdin] and its siblings on a standalone Context, and with
	// [Program.WithStdin] and its siblings for a run. The default reporter writes to the
	// Program's streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Argv is the raw argument vector for this invocation, command names and flags included,
	// for a handler that runs its own parser. It is the live slice, not a copy: mutating it
	// changes what later parses see. A command's declared positionals are the Arguments field
	// of its generated inputs.
	Argv []string

	services      map[string]any
	chain         []Command // resolved command path, root → leaf
	exitCode      int       // first non-zero wins; the reporter overrides
	stopped       bool      // forward progress halts
	exitNow       bool      // hard Exit: skip remaining teardown too
	reporterStage bool      // the reporter is executing: Exit overrides, HaltWithCode is a no-op
	infos         []string  // the outcome channels; they surface only as the Outcome
	errs          []error   // handed to the reporter. faults are captured by the
	warnings      []error   // runtime, never recorded by a handler.
	successes     []string
	faults        []*PanicError

	// frame is the chain index of the command whose hook is running, or frameUnset outside a
	// hook (resolving to the leaf). [AsCommand] sets it around every step; [Context.Command]
	// and the inputs methods anchor on it.
	frame int

	// ctx is the context the running hook received; see [Context.SetContext]. ctxFrozen makes
	// SetContext a no-op during teardown and the reporter.
	ctx       context.Context
	ctxFrozen bool

	// rotini's own settings, seeded from the Program each run and kept out of services so a
	// dependency can never shadow them.
	meta     *InputSettings
	readerFn func(InputSettings) *InputReader
	version  string
	help     HelpFunc
	parser   *Parser

	// view is the environment and directory this run reads; see [Program.WithEnviron] and
	// [Program.WithDir]. nil reads the process's.
	view *osView

	// outputChecks makes WriteOutput and WriteOutputItem check each value against the declared
	// output schema before writing it. See [Program.WithOutputChecks].
	outputChecks bool

	// completionMessages collects what [Context.AddCompletionMessage] adds. It is non-nil only
	// while a completion request runs with completion messages on.
	completionMessages *[]string
	// completionOptions collects what [Context.SetCompletionOptions] asks for. It is non-nil
	// only while a completion request runs.
	completionOptions *CompletionOptions

	// stdinRead is the run's stdin, read at most once and shared by every consumer: argv can
	// be parsed several times in one run and inputs collected more than once, but stdin can be
	// read only once. runState is the run a blocking stdin read watches for cancellation.
	stdinRead *stdinState
	runState  *stdinRun
}

// AddCompletionMessage adds a line for the shell to show while it completes, from a
// [FlagValueCompleter] or [ArgValueCompleter]: why there is nothing to offer, or how to narrow
// a long list. Messages show in the order added, alongside any candidates, and in place of the
// input's static message. Each is reduced to one plain line.
//
//	services, err := loadServices()
//	if err != nil {
//		rtx.AddCompletionMessage("could not read deploy.yaml: " + err.Error())
//		return nil
//	}
//
// It does nothing outside a completion request, or when the conf's completion feature doesn't
// turn messages on. zsh and bash 4.4 or later show messages; other shells skip them.
func (rtx *Context) AddCompletionMessage(msg string) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.completionMessages != nil {
		*rtx.completionMessages = append(*rtx.completionMessages, msg)
	}
}

// argvAcq is what parsing this run's argv reads values from: the replayed stdin and the
// injected directory.
func (rtx *Context) argvAcq() argvAcq {
	return argvAcq{stdin: rtx.flagStdin(), dir: rtx.osView().base()}
}

// cloneServices copies the dependency store. Each run is seeded from the Program's copy, so a
// dependency set during a run stays local to it.
func (rtx *Context) cloneServices() map[string]any {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return maps.Clone(rtx.services)
}

// newContext returns an empty [Context] with the os streams and no resolved command.
func newContext() *Context {
	return &Context{
		services: make(map[string]any),
		frame:    frameUnset,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
}

// NewContextFor builds a [Context] with argv resolved against def, as the runtime does before
// dispatch, using the os streams until [Context.WithStdout] and its siblings replace them, and no
// program settings. It serves tests of the [Parser], the [Context.Inputs] family, or a single
// hook:
//
//	def := rotini.Definition{Name: "app", Handler: "App", Commands: []rotini.CommandDef{ … }}
//	rtx := rotini.NewContextFor(def, []string{"build", "x.yaml"})
//	var in appInputs
//	err := rtx.Parser().Parse(rtx, &in)
//
// A single hook runs against captured streams:
//
//	var out bytes.Buffer
//	rtx := rotini.NewContextFor(def, []string{"list", "-o", "json"}).
//		WithStdin(strings.NewReader("")).
//		WithStdout(&out)
//	(&listHandler{}).Run(t.Context(), rtx)
//
// To test a generated program end to end, build it with the generated NewProgram and run it
// with [Program.WithExit] and captured streams.
//
// A plugin token resolves to the chain that precedes it; no plugin is executed.
func NewContextFor(def Definition, argv []string) *Context {
	rtx := newContext()
	chain, _ := resolveChain(def, argv)
	markInvoked(chain)
	rtx.Argv = argv
	rtx.chain = chain
	return rtx
}

// CommandChain returns every command of this invocation from the root (index 0) to the
// invoked command (the last entry, the only one with Invoked set).
//
// The slice is a copy; changing it does not affect the run. The copy is shallow: each
// command's Flags, Arguments and Commands are the [Definition]'s own slices, shared by every
// run, and must be treated as read-only.
func (rtx *Context) CommandChain() []Command {
	return slices.Clone(rtx.chain)
}

// invokedCommand is the last command of the chain, whichever hook is running.
func (rtx *Context) invokedCommand() Command {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.chain) == 0 {
		return Command{}
	}
	return rtx.chain[len(rtx.chain)-1]
}

// commandName is the canonical path of the invoked command, ancestors included: "taskr list".
func (rtx *Context) commandName() string {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	names := make([]string, 0, len(rtx.chain))
	for _, c := range rtx.chain {
		names = append(names, c.Name)
	}
	return strings.Join(names, " ")
}

// markInvoked sets Invoked on the last command of chain and clears it on the others, whoever
// built the chain.
func markInvoked(chain []Command) {
	for i := range chain {
		chain[i].Invoked = i == len(chain)-1
	}
}

// frameUnset marks a Context that is not inside a lifecycle step. It resolves to the invoked
// command.
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
// Invoked distinguishes the command the user ran from its ancestors:
//
//	func (*songsHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
//	    if rtx.Command().Invoked {
//	        // `musak songs`: this command is the invocation.
//	        return
//	    }
//	    // `musak songs list`: a sub-command is running.
//	}
//
// [Context.Inputs], [Context.CommandPath] and [Context.Help] all describe the command Command
// returns. Outside a lifecycle step (a Context from [NewContextFor], or in the reporter),
// Command returns the invoked command.
//
// # Goroutines
//
// Command reports the step running when it is called, not the hook that started the caller.
// A goroutine that outlives its hook may therefore see a later command, and inputs it reads
// anchor there too. Capture what it needs before starting it:
//
//	cmd := rtx.Command()
//	go func() { log.Println(cmd.Name) }()
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

// frameIndexLocked resolves frameUnset to the invoked command. The caller holds the lock.
func (rtx *Context) frameIndexLocked() int {
	if rtx.frame < 0 || rtx.frame >= len(rtx.chain) {
		return max(len(rtx.chain)-1, 0)
	}
	return rtx.frame
}

// setFrame records which command's hook is running and returns the previous value for
// [AsCommand] to restore.
func (rtx *Context) setFrame(i int) int {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	prev := rtx.frame
	rtx.frame = i
	return prev
}

// CommandPath returns the path of [Context.Command], space-joined from the root: "tasks add"
// for a sub-command, "tasks" for the root. In the root's cascading hook during `tasks add` it
// is "tasks".
//
// The names are canonical, not aliases; each command's Matched field in
// [Context.CommandChain] holds the token the user typed.
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

// Halt stops the lifecycle's forward progress without setting an exit code or recording
// anything. Teardown is unaffected: every PostRun and CascadingPostRun whose setup hook began
// still runs, in reverse. Under the default plan:
//
//	Hook                Effect of Halt
//	────                ──────────────
//	CascadingPreRun     no further setup, no PreRun, no Run
//	PreRun              Run is skipped
//	Run                 none: Run is the last forward step
//	PostRun             none: teardown runs to completion
//	CascadingPostRun    none
//
// Use Halt to stop when nothing failed:
//
//	if !inputs.Force && !confirmed {
//	    rtx.Halt()
//	    return
//	}
//
// Use [Context.HaltWith] to fail, [Context.HaltWithCode] to stop with a specific code, and
// [Context.Exit] to stop without teardown. Halt is a no-op inside the reporter.
func (rtx *Context) Halt() {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.reporterStage {
		return
	}
	rtx.stopped = true
}

// HaltWith records err and stops the lifecycle's forward progress: [Context.RecordError] and
// [Context.Halt] in one call. It sets no exit code; the reporter decides it.
//
//	if err := store.Save(task); err != nil {
//	    rtx.HaltWith(err)
//	    return
//	}
//
// It is the standard way for any hook to fail. Where Halt has no effect (Run and the teardown
// hooks), HaltWith only records. A nil err records nothing and still halts. To record and
// continue, use RecordError alone.
//
// Inside the reporter it has no effect: the halt is a no-op and the record is dropped.
func (rtx *Context) HaltWith(err error) {
	rtx.RecordError(err)
	rtx.Halt()
}

// HaltWithCode sets the exit code and stops the lifecycle's forward progress, for a code that
// carries meaning: a filter reporting "no match" as 1, a wrapper passing through a child's
// status. It records no error.
//
// Teardown is unaffected, as with [Context.Halt]. The first non-zero code wins, so a later
// HaltWithCode or [Context.Exit] does not replace an earlier code.
//
//	Halt()              stop
//	HaltWith(err)       stop and record err
//	HaltWithCode(n)     stop and set exit code n
//	Exit(n)             stop, set exit code n, and skip pending teardown
//
// It is a no-op inside the reporter; the reporter sets the code with [Context.Exit].
func (rtx *Context) HaltWithCode(code int) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.reporterStage {
		return
	}
	rtx.stopped = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// Exit sets the exit code and stops the lifecycle immediately, skipping every pending
// teardown hook, as [os.Exit] skips deferred calls. Prefer [Context.HaltWithCode] for an
// orderly stop. It records no error. A panic recovered before Exit still reaches the reporter.
//
// During the lifecycle the first non-zero code wins; inside the reporter, Exit overrides any
// code already set.
func (rtx *Context) Exit(code int) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.stopped = true
	rtx.exitNow = true
	if rtx.reporterStage {
		rtx.exitCode = code // the reporter is the final authority
		return
	}
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// stopState reports whether forward progress has stopped and whether [Context.Exit] asked to
// skip the remaining teardown.
func (rtx *Context) stopState() (stopped, exitNow bool) {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.stopped, rtx.exitNow
}

// code returns the exit code set so far.
func (rtx *Context) code() int {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.exitCode
}

// setReporterStage marks the start or end of the reporter, inside which Exit overrides the code
// and the halt methods are no-ops.
func (rtx *Context) setReporterStage(on bool) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.reporterStage = on
}

// applyExitFloor sets the exit code to 1 when the run failed and no code was set, and returns
// the resulting code. A deliberate non-zero code is never changed.
func (rtx *Context) applyExitFloor(failed bool) int {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.exitCode == 0 && failed {
		rtx.exitCode = 1
	}
	return rtx.exitCode
}

// RecordInfo records msg as an informational message for the reporter. Like every record
// method it neither prints nor stops the lifecycle. An empty msg is ignored.
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

// RecordError records err as one of this run's errors. It neither prints nor stops the
// lifecycle, so a handler can record several errors before stopping, or leave the decision to
// a later hook that checks [Context.Failed]. A nil err is ignored.
//
//	for _, path := range inputs.Check.Arguments.Paths {
//	    if err := validate(path); err != nil {
//	        rtx.RecordError(err)
//	    }
//	}
//
// To record and stop in one call, use [Context.HaltWith].
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

// RecordWarning records warn as a non-fatal warning: a deprecation, a fallback, a skipped
// item. It is an error so a reporter can inspect it with errors.As or [CategoryOf]; it never
// affects the exit code. A nil warn is ignored.
func (rtx *Context) RecordWarning(warn error) {
	if warn == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.warnings = append(rtx.warnings, warn)
}

// RecordSuccess records msg as a success message for the reporter. An empty msg is ignored.
// The reporter runs after the lifecycle, so records appear after anything a handler wrote
// directly to [Context.Stdout].
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

// Failed reports whether this run has so far recorded an error or captured a fault. A
// teardown hook uses it to decide between committing and rolling back:
//
//	func (*migrateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
//	    if rtx.Failed() {
//	        tx.Rollback()
//	        return
//	    }
//	    tx.Commit()
//	}
//
// The errors themselves reach only the reporter, through [Outcome]. Once true, Failed stays
// true for the rest of the run.
func (rtx *Context) Failed() bool {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return len(rtx.errs) > 0 || len(rtx.faults) > 0
}

// recordFault appends a recovered panic or detected fault.
func (rtx *Context) recordFault(pe *PanicError) {
	if rtx == nil || pe == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.faults = append(rtx.faults, pe)
}

// ── rotini's own settings, set on a standalone Context ──────────────────────.

// WithInputSettings supplies the generated descriptor [Context.Inputs] reads from. See
// [Program.WithInputSettings]. It is for a Context built with [NewContextFor]; during a run,
// the change lasts for the rest of that run.
func (rtx *Context) WithInputSettings(meta InputSettings) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.meta = &meta
		rtx.mu.Unlock()
	}
	return rtx
}

// WithInputReader replaces the input reader [Context.Inputs] uses. See
// [Program.WithInputReader]. It is for a Context built with [NewContextFor]; during a run, the
// change lasts for the rest of that run. A nil fn restores the default, [NewInputReader].
func (rtx *Context) WithInputReader(fn func(InputSettings) *InputReader) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.readerFn = fn
		rtx.mu.Unlock()
	}
	return rtx
}

// WithVersion sets what [Context.Version] reports. See [Program.WithVersion]. It is for a
// Context built with [NewContextFor]; during a run, the change lasts for the rest of that run.
func (rtx *Context) WithVersion(version string) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.version = version
		rtx.mu.Unlock()
	}
	return rtx
}

// WithHelp sets where [Context.Help] finds pages. See [Program.WithHelp]. It is for a Context
// built with [NewContextFor]; during a run, the change lasts for the rest of that run. A nil
// help restores the default, no pages.
func (rtx *Context) WithHelp(help HelpFunc) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.help = help
		rtx.mu.Unlock()
	}
	return rtx
}

// WithParser sets the parser [Context.Parser] returns. [Parser] has no options, so this changes
// nothing today; see [Program.WithParser]. It is for a Context built with [NewContextFor];
// during a run, the change lasts for the rest of that run. A nil parser restores the default.
func (rtx *Context) WithParser(parser *Parser) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.parser = parser
		rtx.mu.Unlock()
	}
	return rtx
}

// WithStdin sets the stream [Context.Stdin] reads and stdin inputs decode, for a Context built
// with [NewContextFor]. A nil reader restores os.Stdin. During a run it changes only what this
// run's hooks and a custom reporter read; redirect a whole run with [Program.WithStdin].
// Configure it before anything reads stdin.
func (rtx *Context) WithStdin(r io.Reader) *Context {
	if rtx != nil {
		if r == nil {
			r = os.Stdin
		}
		rtx.resetStdin(r)
	}
	return rtx
}

// WithStdout sets [Context.Stdout], where handlers and [Context.WriteOutput] write, for a
// Context built with [NewContextFor]. A nil writer restores os.Stdout. During a run it changes
// only this run's later handler writes and what a custom reporter writes to; redirect a whole
// run with [Program.WithStdout]. Configure it before use.
func (rtx *Context) WithStdout(w io.Writer) *Context {
	if rtx != nil {
		if w == nil {
			w = os.Stdout
		}
		rtx.mu.Lock()
		rtx.Stdout = w
		rtx.mu.Unlock()
	}
	return rtx
}

// WithStderr sets [Context.Stderr] for a Context built with [NewContextFor]. A nil writer
// restores os.Stderr. During a run it changes only this run's later handler writes and what a
// custom reporter writes to; the default reporter keeps the Program's stream. Redirect a whole
// run with [Program.WithStderr]. Configure it before use.
func (rtx *Context) WithStderr(w io.Writer) *Context {
	if rtx != nil {
		if w == nil {
			w = os.Stderr
		}
		rtx.mu.Lock()
		rtx.Stderr = w
		rtx.mu.Unlock()
	}
	return rtx
}

// WithOutputChecks makes [Context.WriteOutput] and [Context.WriteOutputItem] check each value
// against the command's declared output schema before writing it, as
// [Program.WithOutputChecks] does for a run. It is for a Context built with [NewContextFor];
// during a run, the change lasts for the rest of that run.
func (rtx *Context) WithOutputChecks(enabled bool) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.outputChecks = enabled
		rtx.mu.Unlock()
	}
	return rtx
}

// ── rotini's own settings, as the handler sees them ─────────────────────────.

// Help returns the help page of [Context.Command] from [Program.WithHelp], as a generated
// --help prints it, or "" when there is none. A cascading hook gets its own command's page.
// The page is the running program's, so a composed command shows its full path and inherited
// flags.
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

// Version returns the version set by [Program.WithVersion], or "" if none was set.
func (rtx *Context) Version() string {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.version
}

// Parser returns the [Parser] set by [Program.WithParser], or a new default parser. It never
// returns nil.
func (rtx *Context) Parser() *Parser {
	rtx.mu.RLock()
	p := rtx.parser
	rtx.mu.RUnlock()
	if p == nil {
		return NewParser()
	}
	return p
}

// settingsForRun returns the run's input descriptor and whether one was supplied.
func (rtx *Context) settingsForRun() (InputSettings, bool) {
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
