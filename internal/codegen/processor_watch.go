package codegen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-rotini/fs"
)

// runOrWatch runs pass once, or with watch re-runs it whenever a file it reads changes until
// interrupted or terminated, sending each result to onResult. confPath must already be
// resolved. Without watch, a failed pass's error is returned instead of reported.
func runOrWatch(specPath, confPath string, watch bool, pass func() (string, error), onResult func(result string, err error)) error {
	if watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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

// watchDebounce coalesces the burst of filesystem events an editor emits on save.
const watchDebounce = 200 * time.Millisecond

// watchConfPoll is how often watch mode checks for a conf that doesn't exist yet.
const watchConfPoll = time.Second

// watchLoop runs pass once and again on each change to the spec, the conf, or any other file
// the last pass read (composed specs, seeded templates), sending every result to onResult,
// until ctx is done. A conf that doesn't exist yet (confPath, or with confPath "" the first
// .rotini.conf.* beside the spec) is polled for and watched once it appears. It returns only
// the spec's watcher setup error; cancellation returns nil.
func watchLoop(ctx context.Context, specPath, confPath string, pass func() (string, error), onResult func(result string, err error)) error {
	ws := &watchSet{ctx: ctx, changed: make(chan struct{}, 1), watchers: map[string]*fs.Watcher{}}
	defer ws.close()
	if err := ws.add(specPath); err != nil {
		return err
	}
	conf := func() string {
		if confPath != "" {
			return confPath
		}
		return resolveConfBesideSpec(specPath, "")
	}
	watchConf := func() bool {
		c := conf()
		if c == "" {
			return false
		}
		if _, err := os.Stat(c); err != nil {
			return false
		}
		return ws.add(c) == nil
	}
	run := func() {
		var result string
		var err error
		stale := false
		for p, read := range recordReads(func() { result, err = pass() }) {
			added, werr := ws.addNew(p, true)
			if werr != nil || !added {
				continue // best effort: a file that can't be watched just isn't
			}
			// The new watcher only sees changes from now on; one made since the pass read
			// the file needs another pass.
			if stampFile(p) != read {
				stale = true
			}
		}
		onResult(result, err)
		if stale {
			ws.signal()
		}
	}

	confWatched := watchConf()
	run()

	poll := time.NewTicker(watchConfPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ws.changed:
			run()
		case <-poll.C:
			if !confWatched && watchConf() {
				confWatched = true
				run()
			}
		}
	}
}

// watchSet is the set of files watch mode watches, each forwarding its changes to changed.
type watchSet struct {
	ctx      context.Context
	changed  chan struct{}
	watchers map[string]*fs.Watcher // by absolute path
}

// add starts watching path, once.
func (ws *watchSet) add(path string) error {
	_, err := ws.addNew(path, false)
	return err
}

// addNew starts watching path unless it already is, reporting whether it started a watcher.
// With missing, a path that doesn't exist is watched for its creation: the path itself, or the
// first of its directories that doesn't exist yet.
func (ws *watchSet) addNew(path string, missing bool) (bool, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	lazy := false
	if _, err := os.Stat(path); missing && errors.Is(err, os.ErrNotExist) {
		path, lazy = firstMissing(path), true
	}
	if _, ok := ws.watchers[path]; ok {
		return false, nil
	}
	newWatcher := fs.NewWatcher
	if lazy {
		newWatcher = fs.NewLazyWatcher
	}
	w, err := newWatcher(path, fs.WithDebounce(watchDebounce))
	if err != nil {
		return false, fmt.Errorf("watch %s: %w", path, err)
	}
	events, err := w.Subscribe(ws.ctx)
	if err != nil {
		_ = w.Close()
		return false, fmt.Errorf("watch %s: %w", path, err)
	}
	ws.watchers[path] = w
	go forwardChanges(ws.ctx, events, ws.changed)
	return true, nil
}

// firstMissing returns path, or the first of its directories that doesn't exist: the one whose
// parent does.
func firstMissing(path string) string {
	for {
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		if _, err := os.Stat(parent); err == nil {
			return path
		}
		path = parent
	}
}

// signal asks for another pass, unless one is already pending.
func (ws *watchSet) signal() {
	select {
	case ws.changed <- struct{}{}:
	default:
	}
}

// close stops every watcher.
func (ws *watchSet) close() {
	for _, w := range ws.watchers {
		_ = w.Close()
	}
}

// reportTiming is the last line of every init, generate and validate report: the wall-clock
// time the run started and how long it took, as "[15:04:05] 2.79ms".
func reportTiming(start time.Time) string {
	return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start)))
}

// roundDuration rounds d to three significant figures for a compact String() (312ns,
// 45.7µs, 2.79ms, 1.23s).
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

// forwardChanges forwards one watcher's events into changed, keeping at most one pending
// signal, until events closes or ctx is canceled.
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
			default: // a pass is already pending
			}
		}
	}
}
