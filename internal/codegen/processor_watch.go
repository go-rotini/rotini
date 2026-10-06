package codegen

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/go-rotini/fs"
)

// runOrWatch runs pass once, or with watch re-runs it on every spec or conf change until
// interrupted, sending each result to onResult. confPath must already be resolved. Without
// watch, a failed pass's error is returned instead of reported.
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

// watchDebounce coalesces the burst of filesystem events an editor emits on save.
const watchDebounce = 200 * time.Millisecond

// watchLoop runs pass once and again on each spec or conf change, sending every result to
// onResult, until ctx is done. It returns only watcher setup errors; cancellation returns nil.
func watchLoop(ctx context.Context, specPath, confPath string, pass func() (string, error), onResult func(result string, err error)) error {
	// The conf is watched only if it exists.
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

	onResult(pass())

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
			onResult(pass())
		}
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
