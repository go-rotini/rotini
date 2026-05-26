package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-rotini/fs"
)

// watchDebounce coalesces the burst of filesystem events most editors emit when
// saving a file (write + chmod + the rename of an atomic save).
const watchDebounce = 200 * time.Millisecond

// GenerateWatch generates once from the spec at specPath (and the conf at
// confPath, or the one discovered next to the spec when confPath is empty), then
// watches the spec and conf files and re-generates whenever either changes, until
// ctx is cancelled. It returns nil on clean cancellation.
//
// A failed pass (e.g. a malformed save) is reported to out and watching
// continues, so the file can be fixed in place without restarting. Progress is
// written to out. ctx is the cancellation handle the caller wires to a signal —
// e.g. SIGINT cancels ctx, this returns, and the lifecycle teardown runs.
func GenerateWatch(ctx context.Context, specPath, confPath string, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
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

	regenerate(specPath, confPath, out) // initial pass
	fmt.Fprintf(out, "rotini: watching %s (ctrl-c to stop)\n", strings.Join(paths, ", "))

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "rotini: stopped watching")
			return nil
		case <-changed:
			regenerate(specPath, confPath, out)
		}
	}
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

// regenerate runs one generation pass, reporting the outcome to out without
// aborting the watch on failure.
func regenerate(specPath, confPath string, out io.Writer) {
	if err := generateOnce(specPath, confPath); err != nil {
		fmt.Fprintf(out, "rotini: %v\n", err)
		return
	}
	fmt.Fprintln(out, "rotini: generated")
}
