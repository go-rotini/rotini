package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/go-rotini/fs"
)

// Generate reads the rotini spec at specPath and the conf at confPath, then
// emits the generated program files. confPath may be empty (or point at a file
// that does not exist), in which case the sane rotini conf defaults are used.
//
// A pass produces three things, mirroring the "commands all the way down" model
// where a root command owns sub-commands that own their own sub-commands:
//
//  1. The framework file (default rtg/rotini.go): the ProgramHandlers aggregate
//     interface plus the typed Flags/Arguments/CommandInputs/Inputs structs for
//     the root command and every sub-command. Always (over)written.
//  2. A handler stub per command in the handler package, named after the
//     command path to avoid collisions (rotini.go, rotini_generate.go, …).
//     Created only when missing, since stubs hold user code.
//  3. The handler rollup file (default handlers.go): the handlers struct, the
//     Program var, and one method per command. Always (over)written; orphaned
//     stubs are pruned when generate.cmd.prune is enabled.
func Generate(specPath, confPath string) error {
	return generateOnce(specPath, resolveConfPath(specPath, confPath))
}

// resolveConfPath returns confPath when set, otherwise the .rotini.conf.* file
// discovered next to the spec (e.g. for the scaffolded `//go:generate rotini
// generate`), or "" when none is found. Watch mode also uses this to learn which
// conf file, if any, to watch.
func resolveConfPath(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	if p, err := discoverFile(filepath.Dir(specPath), ".rotini.conf."); err == nil {
		return p
	}
	return ""
}

// generateOnce runs a single generation pass against an already-resolved conf
// path (which may be empty or point at a missing file, meaning "use defaults").
func generateOnce(specPath, confPath string) error {
	spec, err := ReadSpec(specPath)
	if err != nil {
		return err
	}
	conf, err := loadConfOrDefaults(confPath)
	if err != nil {
		return err
	}
	applyConfDefaults(conf)
	return generateAll(spec, conf, specPath)
}

// loadConfOrDefaults reads the conf at confPath, treating an empty path or a
// missing file as "no conf supplied" and returning an empty *Conf so that
// applyConfDefaults can fill in the defaults.
func loadConfOrDefaults(confPath string) (*Conf, error) {
	if confPath == "" {
		return &Conf{}, nil
	}
	if _, err := os.Stat(confPath); err != nil {
		if os.IsNotExist(err) {
			return &Conf{}, nil
		}
		return nil, fmt.Errorf("stat conf %s: %w", confPath, err)
	}
	return ReadConf(confPath)
}

// watchDebounce coalesces the burst of filesystem events most editors emit when
// saving a file (write + chmod + the rename of an atomic save).
const watchDebounce = 200 * time.Millisecond

// GenerateWatch generates once from the spec at specPath (and the conf at
// confPath, or the one discovered next to the spec when confPath is empty), then
// watches the spec and conf files and re-generates whenever either changes, until
// interrupted with ctrl-c (SIGINT). It returns nil on a clean interrupt.
//
// onGenerate is called after every generation pass — the initial one and each
// re-generation triggered by a change — with a summary line ("[HH:MM:SS] <took>")
// and that pass's error (nil on success). GenerateWatch prints nothing itself, so
// the caller owns all output; watching continues whatever a pass returns, so a
// malformed save can be fixed in place. (A failure to set up the file watchers is
// fatal and returned instead.)
func GenerateWatch(specPath, confPath string, onGenerate func(result string, err error)) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return watch(ctx, specPath, confPath, onGenerate)
}

// watch is the cancelable core of [GenerateWatch]: it runs until ctx is done. It
// is split out so tests can drive it with a context rather than a real signal.
func watch(ctx context.Context, specPath, confPath string, onGenerate func(result string, err error)) error {
	confPath = resolveConfPath(specPath, confPath)

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

	onGenerate(generateTimed(specPath, confPath)) // initial pass

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
			onGenerate(generateTimed(specPath, confPath))
		}
	}
}

// generateTimed runs one generation pass and returns a "[HH:MM:SS] <took>"
// summary alongside the pass's error, so a watcher can report both when a
// generation ran and how long it took. The elapsed time renders in whatever unit
// fits best (ns/µs/ms/s).
func generateTimed(specPath, confPath string) (string, error) {
	start := time.Now()
	err := generateOnce(specPath, confPath)
	result := fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start)))
	return result, err
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
