package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/go-rotini/fs"
)

// GenerateFn is the signature of [Processor.Generate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double.
type GenerateFn = func(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error

// Generate is a convenience over [Processor.Generate]: it builds a Processor for
// version and runs the generate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (init and tests).
func Generate(specPath, confPath string, watch bool, version string, onGenerate func(result string, err error)) error {
	return NewProcessor(version).Generate(specPath, confPath, watch, onGenerate)
}

// generate runs the generator phase over a loaded session (call load — and, through
// the pass, validate — first: invalid input must never reach codegen). It applies the
// built-in conf defaults, then emits the cli and cligen packages plus the enabled doc
// features.
//
// NOTE: the codegen implementation still lives in codegen.go / compose.go / help.go /
// output.go / completion_script.go (reached via generateAll). Those are the next files
// to be absorbed into the generator op, decomposing into a program generator + a
// featureGenerator per doc feature (see ROTINI_PROCESSOR_REFACTOR.md §5).
func (s *session) generate() error {
	applyConfDefaults(s.conf.conf, s.spec.spec.Command.Name)
	return generateAll(s.spec.spec, s.conf.conf, s.spec.path)
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
// one pending signal, until events closes or ctx is cancelled.
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
