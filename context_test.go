package rotini

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestGet_typed(t *testing.T) {
	rtx := NewContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)

	if got, ok := Get[*bytes.Buffer](rtx, "buf"); !ok || got != buf {
		t.Fatalf("Get[*bytes.Buffer] = (%v, %v), want the bound buffer", got, ok)
	}
	if _, ok := Get[*bytes.Buffer](rtx, "missing"); ok {
		t.Error("Get of an unbound key should be ok=false")
	}
	if _, ok := Get[*int](rtx, "buf"); ok {
		t.Error("Get of a wrong-typed binding should be ok=false")
	}
}

func TestBindIfAbsent_keepsExistingElseRegistersDefault(t *testing.T) {
	rtx := NewContext()
	injected, def := &bytes.Buffer{}, &bytes.Buffer{}

	// A prior binding (e.g. a test's double) is kept — BindIfAbsent no-ops.
	rtx.Bind("buf", injected)
	rtx.BindIfAbsent("buf", def)
	if MustGet[*bytes.Buffer](rtx, "buf") != injected {
		t.Error("BindIfAbsent must not overwrite an existing binding")
	}

	// An absent key gets the default registered into the registry.
	rtx.BindIfAbsent("other", def)
	if MustGet[*bytes.Buffer](rtx, "other") != def {
		t.Error("BindIfAbsent should register the default when the key is absent")
	}
}

func TestMustGet_returnsBoundService(t *testing.T) {
	rtx := NewContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)
	if MustGet[*bytes.Buffer](rtx, "buf") != buf {
		t.Fatal("MustGet did not return the bound service")
	}
}

func TestMustGet_panicsServiceError(t *testing.T) {
	rtx := NewContext() // nothing bound

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok {
			t.Fatalf("MustGet panicked with %v (%T), want an error", r, r)
		}
		if !errors.Is(err, ErrServiceNotFound) {
			t.Errorf("panic = %v, want it to wrap ErrServiceNotFound", err)
		}
		var se *ServiceError
		if !errors.As(err, &se) || se.Key != "missing" {
			t.Errorf("panic did not carry the key: %v", err)
		}
	}()

	_ = MustGet[*bytes.Buffer](rtx, "missing") // panics → recovered above
}

func TestMustGet_panicsOnWrongType(t *testing.T) {
	rtx := NewContext()
	rtx.Bind("buf", &bytes.Buffer{})

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet of a wrong-typed binding should panic")
		}
	}()

	_ = MustGet[*int](rtx, "buf") // bound, but not an *int → panics
}

// The registry is the DI seam shared by every hook, and concurrent tooling (tickers,
// completers, a handler's own goroutines) may read and write it at once. This hammers
// Bind/Has/Value/Get from many goroutines so the race detector proves the RWMutex
// guards every path, and confirms all bindings survive the storm.
func TestContext_concurrentRegistry(t *testing.T) {
	rtx := NewContext()
	const (
		workers = 64
		keys    = 8
		iters   = 250
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("svc-%d", w%keys)
			for i := 0; i < iters; i++ {
				rtx.Bind(key, w)
				_ = rtx.Has(key)
				_ = rtx.Value(key)
				_, _ = Get[int](rtx, key)
			}
		}(w)
	}
	wg.Wait()

	for k := 0; k < keys; k++ {
		if !rtx.Has(fmt.Sprintf("svc-%d", k)) {
			t.Errorf("key svc-%d unbound after concurrent access", k)
		}
	}
}
