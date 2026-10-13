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

// Watch-mode tests pin that watchLoop passes once up front, re-passes on change, and returns
// nil on cancellation. They drive it with a context rather than a real signal.

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

// TestWatchLoop_initialPassThenCancel pins one immediate pass and a nil return on cancel.
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

// TestWatchLoop_rerunsOnChange pins that editing the spec runs the pass again.
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

// TestWatchLoop_missingConfIsNotWatched pins that a nonexistent conf does not fail watch
// setup; the spec alone is watched.
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

// TestWatchLoop_unwatchablePathErrors pins that an unwatchable spec is a returned setup
// error.
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

// TestForwardChanges_coalesces pins that events arriving while a signal is pending fold into
// it. The burst is delivered and the channel closed before `changed` is drained.
func TestForwardChanges_coalesces(t *testing.T) {
	events := make(chan fs.WatchEvent, 8)
	changed := make(chan struct{}, 1) // capacity 1, exactly as watchLoop allocates it

	for range 5 {
		events <- fs.WatchEvent{Path: "spec.yaml"}
	}
	close(events)

	done := make(chan struct{})
	go func() { forwardChanges(context.Background(), events, changed); close(done) }()

	// The forwarder returns only after handling every buffered event.
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

// TestForwardChanges_stopsOnCancel pins that the forwarder goroutine exits on cancellation.
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

// TestWatchLoop_watchesFilesThePassReads pins that a file the pass read, such as a composed
// spec, is watched, and that a conf created after the watch starts is picked up.
func TestWatchLoop_watchesFilesThePassReads(t *testing.T) {
	specPath, _ := watchFixture(t, false)
	dir := filepath.Dir(specPath)
	child := filepath.Join(dir, "child.yaml")
	if err := os.WriteFile(child, []byte("version: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counter := newPassCounter()
	pass := func() (string, error) {
		if _, _, err := readRaw(child); err != nil {
			t.Error(err)
		}
		return counter.pass()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", pass, func(string, error) {}) }()
	counter.await(t, 1, 5*time.Second)

	if err := os.WriteFile(child, []byte("version: 0.0.0\n# edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counter.await(t, 2, 10*time.Second)

	if err := os.WriteFile(filepath.Join(dir, ".rotini.conf.yaml"), []byte("version: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counter.await(t, 3, 10*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestWatchLoop_watchesFilesThePassFoundMissing pins that a file a pass tried to read before it
// existed, in a directory that exists or not, starts a pass when it is created, and that later
// edits to it do too.
func TestWatchLoop_watchesFilesThePassFoundMissing(t *testing.T) {
	for _, sub := range []string{"", filepath.Join("status", "deeper")} {
		t.Run("dir="+sub, func(t *testing.T) {
			specPath, _ := watchFixture(t, false)
			child := filepath.Join(filepath.Dir(specPath), sub, "child.yaml")
			counter := newPassCounter()
			pass := func() (string, error) {
				_, _, err := readRaw(child)
				counter.pass()
				return "", err
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- watchLoop(ctx, specPath, "", pass, func(string, error) {}) }()
			counter.await(t, 1, 5*time.Second)
			// Let the first pass start its watchers, as a person editing the file would.
			time.Sleep(3 * watchDebounce)

			// Each directory that appears starts a pass too, which watches the next one.
			if err := os.MkdirAll(filepath.Dir(child), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(child, []byte("version: 0.0.0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(15 * time.Second)
			for {
				n := counter.count()
				if _, _, err := readRaw(child); err == nil && n >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("creating the missing file started no pass (%d passes)", n)
				}
				time.Sleep(50 * time.Millisecond)
			}
			// Wait for the passes the creation started, then edit the file.
			time.Sleep(3 * watchDebounce)
			n := counter.count()
			if err := os.WriteFile(child, []byte("version: 0.0.0\n# edited\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			counter.await(t, n+1, 10*time.Second)

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("watchLoop did not return after cancel")
			}
		})
	}
}

// TestWatchLoop_editDuringFirstReadReruns pins that a file changed after a pass first read it,
// but before its watcher started, gets another pass: the pass itself saves the file right
// after reading it, so the watcher's baseline already holds the edit.
func TestWatchLoop_editDuringFirstReadReruns(t *testing.T) {
	specPath, _ := watchFixture(t, false)
	child := filepath.Join(filepath.Dir(specPath), "child.yaml")
	if err := os.WriteFile(child, []byte("version: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counter := newPassCounter()
	pass := func() (string, error) {
		if _, _, err := readRaw(child); err != nil {
			t.Error(err)
		}
		if counter.count() == 0 {
			if err := os.WriteFile(child, []byte("version: 0.0.0\n# saved mid-pass\n"), 0o600); err != nil {
				t.Error(err)
			}
		}
		return counter.pass()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", pass, func(string, error) {}) }()
	counter.await(t, 2, 10*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestRecordReads_stampsFirstRead pins that the read log stamps a file as it was when first
// noted, so a later change compares unequal.
func TestRecordReads_stampsFirstRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reads := recordReads(func() {
		noteRead(path)
		if err := os.WriteFile(path, []byte("a: 12\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		noteRead(path)
	})
	abs, _ := filepath.Abs(path)
	read, ok := reads[abs]
	if !ok || !read.exists || read.size != 5 {
		t.Fatalf("stamp = %+v (recorded %v), want the 5-byte first read", read, ok)
	}
	if stampFile(path) == read {
		t.Error("a changed file still matches its read stamp")
	}
}
