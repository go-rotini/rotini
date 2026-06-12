package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/go-rotini/fs"
)

// Processor is the top-level rotini controller — the value the companion handlers in
// cmd/rotini construct and drive. It holds process-wide state (the running binary
// version, for the $schema guard) and exposes the workflows: Generate, Validate, and
// Initialize. Each workflow resolves the spec/conf paths, then runs — once, or on every
// change in watch mode — building a fresh session per pass (see session; reading and
// discovery live in reader.go, the schema-holding loaders in loader.go).
type Processor struct {
	version string // running binary version string ("vX.Y.Z" / "v0.0.0"; "" → guard skipped)
}

// NewProcessor returns a Processor tagged with the running binary's version string.
func NewProcessor(version string) *Processor {
	return &Processor{version: version}
}

// Generate runs the generate workflow: load → validate (the gate) → emit the program,
// once or — when watch is set — on every spec/conf change until interrupted (ctrl-c).
// onGenerate, which may be nil, receives a "[HH:MM:SS] <took>" summary and each pass's
// error; without watch the single pass's error is returned (not routed), so the caller
// can treat the run as failed.
func (p *Processor) Generate(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error {
	if onGenerate == nil {
		onGenerate = func(string, error) {}
	}
	return p.run(specPath, confPath, "", watch, (*session).generatePass, onGenerate)
}

// Validate runs the validate workflow: load → validate, once or on every change (watch).
// failMode is the --fail override ("fast"/"collect"; "" → the conf's validate.fail).
// onValidate, which may be nil, receives a summary and each pass's error.
func (p *Processor) Validate(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	return p.run(specPath, confPath, failMode, watch, (*session).validatePass, onValidate)
}

// Initialize scaffolds a new rotini CLI named name and generates it.
// format selects the spec/conf serialization; force overwrites the create-once files.
func (p *Processor) Initialize(name, format string, force bool, wire []string) error {
	return p.initialize(name, format, force, wire)
}

// run resolves the spec/conf paths up-front (so watch watches exactly the files read),
// then drives the shared run/watch engine: each pass builds a fresh session (re-reading
// the files, so edits are picked up) and runs pass over it, stamped with a
// "[HH:MM:SS] <took>" summary handed to onResult.
func (p *Processor) run(specPath, confPath, failMode string, watch bool, pass func(*session) error, onResult func(result string, err error)) error {
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
		s := newSession(resolvedSpec, resolvedConf, p.version)
		s.failMode = failMode
		err := pass(s)
		return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

// session is one pass over one spec/conf pair — built fresh per pass (so watch
// re-reads). It loads the files as a specLoader/confLoader (loader.go), validates
// them, and generates the program.
type session struct {
	version  string // for the $schema guard, threaded onto the loaded files
	specPath string // explicit spec path ("" → resolved from the fallback locations)
	confPath string // explicit conf path ("" → resolved beside the spec, else defaults)
	failMode string // --fail override; "" → the conf's validate.fail (then "collect")

	spec *specLoader // loaded by load()
	conf *confLoader // loaded by load()
}

// newSession returns a session for the spec/conf at the given paths (either may be
// empty — load resolves them).
func newSession(specPath, confPath, version string) *session {
	return &session{version: version, specPath: specPath, confPath: confPath}
}

// load reads the end-user spec and conf as a specLoader/confLoader. The spec is loaded
// first because conf resolution looks beside it.
func (s *session) load() error {
	spec, err := newSpecLoader(s.specPath, s.version)
	if err != nil {
		return err
	}
	conf, err := newConfLoader(spec.path, s.confPath, s.version)
	if err != nil {
		return err
	}
	s.spec, s.conf = spec, conf
	return nil
}

// The session's per-op methods live with their op: validate + failFast (validator.go),
// generate (generator.go).

// validatePass runs one pass of the validate workflow: load, then validate.
func (s *session) validatePass() error {
	if err := s.load(); err != nil {
		return err
	}
	return s.validate()
}

// generatePass runs one pass of the generate workflow: load, validate (the gate), then
// generate — so invalid input never reaches codegen.
func (s *session) generatePass() error {
	if err := s.load(); err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	return s.generate()
}

// ─── the run/watch engine ────────────────────────────────────────────────────

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
