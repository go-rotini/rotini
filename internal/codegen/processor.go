package codegen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonschema"
)

// Processor is the rotini controller — the value the companion handlers in cmd/rotini
// construct and drive. It holds the immutable per-process config: the running binary
// version (so a spec/conf can be checked to target THIS rotini) and the compiled
// embedded JSON Schemas. It exposes the workflows Generate, Validate, and Initialize.
//
// Every workflow runs the same staged pipeline over the end-user's documents, each
// stage a receiver method:
//
//	reconcile (read + decode)  →  validate (version + schema)  →  lint (rotini rules)  →  generate
//
// reconcileSpec/reconcileConf return reconciled VALUES that flow through the later
// stages, so the Processor itself stays immutable and one instance safely drives many
// passes (e.g. watch mode re-runs the pipeline on every change).
type Processor struct {
	version    string             // running binary version ("vX.Y.Z" / "v0.0.0"; "" → version check skipped)
	specSchema *jsonschema.Schema // compiled embedded spec JSON Schema
	confSchema *jsonschema.Schema // compiled embedded conf JSON Schema
}

// NewProcessor returns a Processor tagged with the running binary's version string (the
// companion CLI passes a [rotini.Versioner]'s VersionSemantic). It compiles the embedded
// JSON Schemas once; a compile failure is a rotini packaging bug, never user input, so
// it panics rather than surfacing a user-facing error.
func NewProcessor(version string) *Processor {
	specSchema, err := loadSpecSchema()
	if err != nil {
		panic(err)
	}
	confSchema, err := loadConfSchema()
	if err != nil {
		panic(err)
	}
	return &Processor{
		version:    version,
		specSchema: specSchema,
		confSchema: confSchema,
	}
}

// Generate runs the generate workflow: reconcile → validate (the gate) → emit the
// program — once, or on every spec/conf change in watch mode until interrupted (ctrl-c).
// onGenerate (may be nil) receives a "[HH:MM:SS] <took>" summary and each pass's error;
// without watch the single pass's error is returned so the caller can treat it as failed.
func (p *Processor) Generate(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error {
	if onGenerate == nil {
		onGenerate = func(string, error) {}
	}
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		return nil, p.validateAndEmit(rs, rc) // generate surfaces no warnings of its own
	}
	return p.run(specPath, confPath, watch, pass, onGenerate, nil)
}

// Validate runs the validate workflow: reconcile → validate + lint, once or on every
// change (watch). failMode is the --fail override ("fast"/"collect"; "" → the conf's
// validate.fail). onValidate (may be nil) receives a summary and each pass's error;
// onWarnings (may be nil) receives every pass's non-fatal warnings.
func (p *Processor) Validate(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		return p.validateDocuments(rs, rc, failMode)
	}
	return p.run(specPath, confPath, watch, pass, onValidate, onWarnings)
}

// Initialize scaffolds a new rotini CLI named name: it writes + validates the seed
// spec + conf, then runs the standard generate to produce a ready-to-build CLI (see
// initialize). format selects the serialization; force overwrites create-once files.
func (p *Processor) Initialize(name, format string, force bool) error {
	return p.initialize(name, format, force)
}

// ─── the staged pipeline ───────────────────────────────────────────────────────.

// reconcile reads + decodes both documents: the spec is REQUIRED (reconcileSpec errors
// on a missing one), the conf is OPTIONAL (reconcileConf yields the default shape when
// absent). The conf is resolved beside the (now-known) spec path.
func (p *Processor) reconcile(specPath, confPath string) (*reconciledSpec, *reconciledConf, error) {
	rs, err := p.reconcileSpec(specPath)
	if err != nil {
		return nil, nil, err
	}
	rc, err := p.reconcileConf(rs.path, confPath)
	if err != nil {
		return nil, nil, err
	}
	return rs, rc, nil
}

// validateAndLintSpec schema-validates the spec, then — only when it is schema-valid (the lint
// rules assume a valid shape) — lints it. It returns every problem found.
func (p *Processor) validateAndLintSpec(rs *reconciledSpec) []error {
	if problems := p.validateSpec(rs); len(problems) > 0 {
		return problems
	}
	return p.lintSpec(rs)
}

// validateAndLintConf is validateAndLintSpec for the conf.
func (p *Processor) validateAndLintConf(rc *reconciledConf) []error {
	if problems := p.validateConf(rc); len(problems) > 0 {
		return problems
	}
	return p.lintConf(rc)
}

// validateDocuments runs the full validate pass over both documents, honoring fast vs
// collect, returning the pass's non-fatal warnings and a joined fatal error (nil → all
// valid). In fast mode it stops at the first failing document.
func (p *Processor) validateDocuments(rs *reconciledSpec, rc *reconciledConf, failMode string) (warnings []error, err error) {
	fast := failFast(failMode, rc)

	specErrs, specWarns := splitProblems(p.validateAndLintSpec(rs))
	warnings = append(warnings, specWarns...)
	if fast && len(specErrs) > 0 {
		return warnings, specErrs[0]
	}

	confErrs, confWarns := splitProblems(p.validateAndLintConf(rc))
	warnings = append(warnings, confWarns...)
	if fast && len(confErrs) > 0 {
		return warnings, confErrs[0]
	}

	return warnings, errors.Join(append(specErrs, confErrs...)...)
}

// validateAndEmit is the gate-then-emit step: validation must pass (the gate — invalid input
// never reaches codegen), then the conf defaults are applied and the program emitted.
func (p *Processor) validateAndEmit(rs *reconciledSpec, rc *reconciledConf) error {
	if _, err := p.validateDocuments(rs, rc, ""); err != nil {
		return err
	}
	applyConfDefaults(rc.conf, rs.spec.Command.Name)
	return emit(rs.spec, rc.conf, rs.path)
}

// failFast reports whether validation stops at the first problem: the --fail override
// (failMode) wins, else the reconciled conf's validate.fail. Only "fast" enables it —
// anything else collects every problem (the default).
func failFast(failMode string, rc *reconciledConf) bool {
	mode := failMode
	if mode == "" && rc != nil && rc.conf != nil && rc.conf.Validate != nil {
		mode = rc.conf.Validate.Fail
	}
	return mode == "fast"
}

// ─── the run/watch engine ──────────────────────────────────────────────────────.

// run resolves the spec/conf paths up-front (so watch watches exactly the files read),
// then drives the shared run/watch engine: each pass reconciles + processes the files
// fresh (so edits are picked up), stamped with a "[HH:MM:SS] <took>" summary handed to
// onResult. pass returns the pass's non-fatal warnings (forwarded to onWarnings) and
// fatal error.
func (p *Processor) run(specPath, confPath string, watch bool, pass func(specPath, confPath string) (warnings []error, err error), onResult func(result string, err error), onWarnings func([]error)) error {
	resolvedSpec, err := resolveSpecPath(specPath)
	if err != nil {
		return err
	}
	if resolvedSpec == "" {
		return errSpecPathRequired
	}
	resolvedConf := resolveConfBesideSpec(resolvedSpec, confPath)

	timed := func() (string, error) {
		start := time.Now()
		warnings, err := pass(resolvedSpec, resolvedConf)
		// Surface warnings on every pass (success or failure), independent of the
		// pass/fail result onResult carries. nil when the workflow has none (generate).
		if onWarnings != nil && len(warnings) > 0 {
			onWarnings(warnings)
		}
		return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

// runOrWatch performs a single timed pass — returning the pass's error when it fails — or,
// when watch is set, watches the spec and conf and re-runs the pass on each change until
// interrupted with ctrl-c (SIGINT), routing every pass (success or failure) to onResult. It is
// the shared engine behind [Generate] and [Validate]; pass supplies the command-specific work
// and confPath must already be resolved (see resolveConfBesideSpec).
func runOrWatch(specPath, confPath string, watch bool, pass func() (string, error), onResult func(result string, err error)) error {
	if watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return watchLoop(ctx, specPath, confPath, pass, onResult)
	}
	result, err := pass()
	if err != nil {
		return err
	}
	onResult(result, nil)
	return nil
}

// watchDebounce coalesces the burst of filesystem events most editors emit when
// saving a file (write + chmod + the rename of an atomic save).
const watchDebounce = 200 * time.Millisecond

// watchLoop is the cancelable core of watch mode: it runs pass once, then re-runs it on each
// change to the spec or conf, handing every pass to onResult, until ctx is done (a clean
// interrupt → nil). It is split out so tests can drive it with a context rather than a real
// signal. Only a failure to set up the watchers is returned.
func watchLoop(ctx context.Context, specPath, confPath string, pass func() (string, error), onResult func(result string, err error)) error {
	// Watch the spec always, and the conf only when it exists (conf is optional).
	paths := []string{specPath}
	if confPath != "" {
		if _, err := os.Stat(confPath); err == nil {
			paths = append(paths, confPath)
		}
	}

	changed := make(chan struct{}, 1)
	var watchers []*fs.Watcher
	defer func() {
		for _, w := range watchers {
			_ = w.Close()
		}
	}()
	for _, p := range paths {
		w, err := fs.NewWatcher(p, fs.WithDebounce(watchDebounce))
		if err != nil {
			return fmt.Errorf("watch %s: %w", p, err)
		}
		watchers = append(watchers, w)
		events, err := w.Subscribe(ctx)
		if err != nil {
			return fmt.Errorf("watch %s: %w", p, err)
		}
		go forwardChanges(ctx, events, changed)
	}

	onResult(pass()) // initial pass

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
			onResult(pass())
		}
	}
}

// roundDuration trims d to roughly three significant figures so its String()
// stays compact while still rendering in the unit that fits best — ns, µs, ms,
// or s, which Duration.String already selects (e.g. 312ns, 45.7µs, 2.79ms, 1.23s).
func roundDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	unit := time.Nanosecond
	for d/unit >= 1000 {
		unit *= 10
	}
	return d.Round(unit)
}

// forwardChanges fans one watcher's events into changed, coalescing to at most
// one pending signal, until events closes or ctx is canceled.
func forwardChanges(ctx context.Context, events <-chan fs.WatchEvent, changed chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			select {
			case changed <- struct{}{}:
			default: // a regeneration is already pending; fold this change into it
			}
		}
	}
}
