package codegen

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-rotini/fs"
)

// `rotini generate --watch` and `rotini validate --watch` are declared in the companion CLI's
// spec, rendered into its help pages, and were shipped with no test at all — watchLoop and
// forwardChanges sat at 0% coverage. Watch mode is also the feature most likely to be left
// running for hours in someone's terminal, so the three properties that matter are pinned
// here: it passes once up front, it re-passes on a change, and a canceled context ends it
// cleanly rather than hanging or erroring.
//
// watchLoop takes a context precisely so these can be driven without a real signal.

// watchFixture writes a spec (and optionally a conf) into a temp dir and returns their paths.
func watchFixture(t *testing.T, withConf bool) (specPath, confPath string) {
	t.Helper()
	dir := t.TempDir()
	specPath = filepath.Join(dir, ".rotini.spec.yaml")
	if err := os.WriteFile(specPath, []byte("version: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if withConf {
		confPath = filepath.Join(dir, ".rotini.conf.yaml")
		if err := os.WriteFile(confPath, []byte("version: 0.0.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return specPath, confPath
}

// passCounter is a pass function that counts its invocations and signals each one.
type passCounter struct {
	mu     sync.Mutex
	n      int
	passed chan struct{}
}

func newPassCounter() *passCounter {
	return &passCounter{passed: make(chan struct{}, 16)}
}

func (p *passCounter) pass() (string, error) {
	p.mu.Lock()
	p.n++
	p.mu.Unlock()
	select {
	case p.passed <- struct{}{}:
	default:
	}
	return "pass", nil
}

func (p *passCounter) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// await blocks until at least n passes have happened, or the deadline expires.
func (p *passCounter) await(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for p.count() < n {
		select {
		case <-p.passed:
		case <-deadline:
			t.Fatalf("waited %v for %d passes, saw %d", within, n, p.count())
		}
	}
}

// TestWatchLoop_initialPassThenCancel: watch mode runs the pass once immediately, so the
// author sees the result without touching anything, and a canceled context returns nil —
// a clean interrupt is not an error.
func TestWatchLoop_initialPassThenCancel(t *testing.T) {
	specPath, confPath := watchFixture(t, true)
	counter := newPassCounter()

	var results []string
	var mu sync.Mutex
	onResult := func(result string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			t.Errorf("unexpected pass error: %v", err)
		}
		results = append(results, result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, confPath, counter.pass, onResult) }()

	counter.await(t, 1, 5*time.Second)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watchLoop after cancel = %v, want nil — a clean interrupt is not a failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not return after its context was canceled")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		t.Fatal("no pass was routed to onResult")
	}
	if results[0] != "pass" {
		t.Errorf("first result = %q, want the pass's own result", results[0])
	}
}

// TestWatchLoop_rerunsOnChange is the feature itself: editing the spec runs the pass again.
func TestWatchLoop_rerunsOnChange(t *testing.T) {
	specPath, _ := watchFixture(t, false)
	counter := newPassCounter()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", counter.pass, func(string, error) {}) }()

	counter.await(t, 1, 5*time.Second) // the initial pass

	// Rewrite the spec; the watcher's debounce window is watchDebounce.
	if err := os.WriteFile(specPath, []byte("version: 0.0.0\n# edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counter.await(t, 2, 10*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestWatchLoop_missingConfIsNotWatched: the conf is optional, so naming one that does not
// exist must not fail watch setup — the spec alone is still watched.
func TestWatchLoop_missingConfIsNotWatched(t *testing.T) {
	specPath, _ := watchFixture(t, false)
	missingConf := filepath.Join(filepath.Dir(specPath), "absent.conf.yaml")
	counter := newPassCounter()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, missingConf, counter.pass, func(string, error) {}) }()

	counter.await(t, 1, 5*time.Second)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watchLoop with an absent conf = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestWatchLoop_unwatchablePathErrors: a spec that cannot be watched is a setup failure and
// has to be reported rather than silently degrading to a single pass.
func TestWatchLoop_unwatchablePathErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "deeper", ".rotini.spec.yaml")
	counter := newPassCounter()

	err := watchLoop(context.Background(), missing, "", counter.pass, func(string, error) {})
	if err == nil {
		t.Fatal("watching an unwatchable path returned nil, want a setup error")
	}
	if counter.count() != 0 {
		t.Errorf("the pass ran %d times despite a setup failure, want 0", counter.count())
	}
}

// TestForwardChanges_coalesces pins the property that keeps an editor's save burst (write +
// chmod + the rename of an atomic save) from triggering a regeneration each: while a pass is
// still pending, further events fold into the one already queued.
//
// The burst is delivered and the channel closed before anything drains `changed`, so the
// forwarder observes all five with a signal already pending — which is exactly the condition
// a mid-regeneration burst creates.
func TestForwardChanges_coalesces(t *testing.T) {
	events := make(chan fs.WatchEvent, 8)
	changed := make(chan struct{}, 1) // capacity 1, exactly as watchLoop allocates it

	for range 5 {
		events <- fs.WatchEvent{Path: "spec.yaml"}
	}
	close(events)

	done := make(chan struct{})
	go func() { forwardChanges(context.Background(), events, changed); close(done) }()

	// Closing the event channel ends the forwarder, and it only ends once every buffered
	// event has been handled — so this also pins "drains before returning".
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardChanges did not return when its event channel closed")
	}

	if got := len(changed); got != 1 {
		t.Fatalf("five events produced %d pending signals, want exactly 1", got)
	}
	<-changed
	select {
	case <-changed:
		t.Error("a second signal was queued; the burst should have coalesced into one")
	default:
	}
}

// TestForwardChanges_stopsOnCancel: the forwarder is a goroutine per watched file, so it has
// to end with the context or watch mode leaks one per pass.
func TestForwardChanges_stopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { forwardChanges(ctx, make(chan fs.WatchEvent), make(chan struct{}, 1)); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardChanges outlived its context")
	}
}
