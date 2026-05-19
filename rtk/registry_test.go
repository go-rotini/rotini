package rtk_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

func TestNewRegistry(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.Has("anything") {
		t.Error("fresh Registry should not have any keys")
	}
}

func TestRegistry_Bind_Has_Get(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry()

	// Has returns false before binding.
	if r.Has("k") {
		t.Error("Has returned true before Bind")
	}

	// Bind round-trips.
	got := r.Bind("k", "value")
	if got != r {
		t.Error("Bind did not return the receiver (not chainable)")
	}
	if !r.Has("k") {
		t.Error("Has returned false after Bind")
	}
	if v := rtk.Get[string](r, "k"); v != "value" {
		t.Errorf("Get returned %q, want %q", v, "value")
	}
}

func TestRegistry_Bind_chainable(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry().
		Bind("a", 1).
		Bind("b", "two").
		Bind("c", true)

	if v := rtk.Get[int](r, "a"); v != 1 {
		t.Errorf("a: got %d, want 1", v)
	}
	if v := rtk.Get[string](r, "b"); v != "two" {
		t.Errorf("b: got %q, want %q", v, "two")
	}
	if v := rtk.Get[bool](r, "c"); !v {
		t.Error("c: got false, want true")
	}
}

func TestRegistry_Bind_overwrites(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry().Bind("k", "first").Bind("k", "second")
	if v := rtk.Get[string](r, "k"); v != "second" {
		t.Errorf("after rebind, Get returned %q, want %q", v, "second")
	}
}

func TestGet_missingKey_returnsZero(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry()

	if v := rtk.Get[string](r, "missing"); v != "" {
		t.Errorf("Get of missing key: got %q, want empty string", v)
	}
	if v := rtk.Get[int](r, "missing"); v != 0 {
		t.Errorf("Get of missing key: got %d, want 0", v)
	}
	if v := rtk.Get[*Registry](r, "missing"); v != nil {
		t.Errorf("Get of missing key: got %v, want nil", v)
	}
}

// Registry is a placeholder type so Get[*Registry] in the test above compiles
// against this package's *_test.go file.
type Registry struct{}

func TestGet_wrongType_returnsZero(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry().Bind("k", "a string")

	// Bound as string, asked as int — type-assertion fails, zero returned.
	if v := rtk.Get[int](r, "k"); v != 0 {
		t.Errorf("Get of wrong type: got %d, want 0", v)
	}
	// Bound as string, asked as *int — zero (nil).
	if v := rtk.Get[*int](r, "k"); v != nil {
		t.Errorf("Get of wrong type: got %v, want nil", v)
	}
}

func TestGet_interfaceType(t *testing.T) {
	t.Parallel()

	type Greeter interface {
		Greet() string
	}

	r := rtk.NewRegistry().Bind("greeter", greeterImpl{name: "rotini"})
	g := rtk.Get[Greeter](r, "greeter")
	if g == nil {
		t.Fatal("Get returned nil for interface binding")
	}
	if got := g.Greet(); got != "hello, rotini" {
		t.Errorf("Greet: got %q, want %q", got, "hello, rotini")
	}
}

type greeterImpl struct{ name string }

func (g greeterImpl) Greet() string { return "hello, " + g.name }

func TestRegistry_concurrent_Bind_Get(t *testing.T) {
	t.Parallel()

	const goroutines = 64
	const iterations = 1000

	r := rtk.NewRegistry()
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	var writes, reads atomic.Int64

	for i := range goroutines {
		go func() {
			defer wg.Done()
			for j := range iterations {
				r.Bind("shared", i*1000+j)
				writes.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				_ = rtk.Get[int](r, "shared")
				_ = r.Has("shared")
				reads.Add(1)
			}
		}()
	}
	wg.Wait()

	if writes.Load() != int64(goroutines*iterations) {
		t.Errorf("writes: got %d, want %d", writes.Load(), goroutines*iterations)
	}
	if reads.Load() != int64(goroutines*iterations) {
		t.Errorf("reads: got %d, want %d", reads.Load(), goroutines*iterations)
	}
	// And the final value is some valid int — we don't care which goroutine won.
	_ = rtk.Get[int](r, "shared")
}

func TestRegistry_Has_unbound(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry()
	if r.Has("nope") {
		t.Error("Has returned true for unbound key")
	}
	r.Bind("yes", 1)
	if !r.Has("yes") {
		t.Error("Has returned false for bound key")
	}
	if r.Has("nope") {
		t.Error("Has returned true for unbound key after other binds")
	}
}

func TestBuildInfo_zeroValue(t *testing.T) {
	t.Parallel()

	var bi rtk.BuildInfo
	if bi.Version != "" || bi.Commit != "" || bi.Date != "" {
		t.Errorf("zero BuildInfo: got %+v, want all empty", bi)
	}
}

func TestBuildInfo_bindAndRetrieve(t *testing.T) {
	t.Parallel()

	bi := rtk.BuildInfo{Version: "v1.0.0", Commit: "abc123", Date: "2026-05-18"}
	r := rtk.NewRegistry().Bind("build-info", bi)

	got := rtk.Get[rtk.BuildInfo](r, "build-info")
	if got != bi {
		t.Errorf("BuildInfo round-trip: got %+v, want %+v", got, bi)
	}
}

func TestCtx_holdsRegistry(t *testing.T) {
	t.Parallel()

	r := rtk.NewRegistry().Bind("k", 42)
	rtx := rtk.Ctx{Registry: r}

	if got := rtk.Get[int](rtx.Registry, "k"); got != 42 {
		t.Errorf("Ctx.Registry retrieval: got %d, want 42", got)
	}
}
