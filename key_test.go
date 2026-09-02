package rotini

import (
	"context"
	"io"
	"strings"
	"testing"
)

// A typed key exists so the registry string and the type assertion cannot drift, and
// so a handler needs neither. These pin both halves.

type demoStore interface{ All() []string }

type memDemoStore struct{ items []string }

func (m memDemoStore) All() []string { return m.items }

var demoStoreKey = NewKey[demoStore]("demo-store")

func TestKey_provideAndGet(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		store := demoStoreKey.MustGet(rtx)
		if got := store.All(); len(got) != 2 {
			t.Errorf("store.All() = %v, want 2 items", got)
		}
		if _, ok := demoStoreKey.Get(rtx); !ok {
			t.Error("Get reported the key as unbound")
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	demoStoreKey.Provide(p, memDemoStore{items: []string{"a", "b"}})

	if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
}

// A typed key names the same registry slot as the untyped surface, so the two
// interoperate rather than being parallel worlds.
func TestKey_isTheSameRegistrySlot(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		if _, ok := Get[demoStore](rtx, demoStoreKey.Name()); !ok {
			t.Error("a Provide'd value is not reachable through the untyped Get")
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	demoStoreKey.Provide(p, memDemoStore{})
	p.Run([]string{"run", "x"})
}

// A missing service routes to the funnel as a fault rather than a nil dereference —
// the same contract as the untyped MustGet.
func TestKey_missingServiceReachesTheFunnel(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_ = demoStoreKey.MustGet(rtx) // never provided
	}}
	p, _, errb := newTestProgram(h, nil)

	code, _ := p.Run([]string{"run", "x"})
	if code == 0 {
		t.Error("a missing service exited 0")
	}
	if !strings.Contains(errb.String(), demoStoreKey.Name()) {
		t.Errorf("the failure does not name the missing service:\n%s", errb.String())
	}
}

// BindTo scopes a value to one invocation, for a service a hook computes per run.
func TestKey_bindToIsPerRun(t *testing.T) {
	var seen []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_, ok := demoStoreKey.Get(rtx)
		seen = append(seen, ok)
		demoStoreKey.BindTo(rtx, memDemoStore{})
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if len(seen) != 2 || seen[0] || seen[1] {
		t.Errorf("BindTo visibility = %v, want [false false] — a per-run bind must not leak forward", seen)
	}
}

func TestKey_nameAndString(t *testing.T) {
	if demoStoreKey.Name() != "demo-store" || demoStoreKey.String() != "demo-store" {
		t.Errorf("Name/String = %q/%q, want demo-store", demoStoreKey.Name(), demoStoreKey.String())
	}
}

func TestProvide_nilProgram(t *testing.T) {
	if got := demoStoreKey.Provide(nil, memDemoStore{}); got != nil {
		t.Error("Provide on a nil program should return nil rather than panicking")
	}
}

// A key declared over an INTERFACE accepts any value assignable to it — the shape a
// real CLI has, where a constructor returns a concrete type.
func TestKey_acceptsAssignableConcreteType(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	demoStoreKey.Provide(p, memDemoStore{items: []string{"x"}}) // concrete, not demoStore
}

// Command and Path answer "which command am I?" — the question every handler that
// logs, audits, or builds an error message asks.
func TestContext_commandAndPath(t *testing.T) {
	var cmd ResolvedCommand
	var path string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		cmd, path = rtx.Command(), rtx.Path()
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"run", "x"})

	if cmd.Name != "run" {
		t.Errorf("Command().Name = %q, want run", cmd.Name)
	}
	if path != "app run" {
		t.Errorf("Path() = %q, want %q", path, "app run")
	}
}

// An alias reports the CANONICAL name, so a path is stable to log and compare on.
func TestContext_pathIsCanonicalNotTyped(t *testing.T) {
	var path string
	var matched string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		path = rtx.Path()
		matched = rtx.Command().Matched
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"r", "x"}) // "r" is an alias of "run"

	if path != "app run" {
		t.Errorf("Path() via alias = %q, want the canonical %q", path, "app run")
	}
	if matched != "r" {
		t.Errorf("Matched = %q, want the typed token %q", matched, "r")
	}
}

// A bare root invocation still has a command and a path — the root is a command like
// any other, not a special case the caller has to handle.
func TestContext_commandAtRoot(t *testing.T) {
	var cmd ResolvedCommand
	var path string

	p := NewProgram(
		Definition{Name: "app", Handler: "Main"},
		rootOnlyHandlers{capture: func(rtx *Context) { cmd, path = rtx.Command(), rtx.Path() }},
	).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run(nil); err != nil {
		t.Fatal(err)
	}
	if cmd.Name != "app" || path != "app" {
		t.Errorf("root Command/Path = %q/%q, want app/app", cmd.Name, path)
	}
}

// rootOnlyHandlers is a one-command handler set, for asserting on the root frame.
type rootOnlyHandlers struct{ capture func(*Context) }

func (h rootOnlyHandlers) Main() Handlers { return rootOnlyRun(h) }

type rootOnlyRun rootOnlyHandlers

func (rootOnlyRun) CascadingPreRun(context.Context, *Context)  {}
func (rootOnlyRun) PreRun(context.Context, *Context)           {}
func (rootOnlyRun) PostRun(context.Context, *Context)          {}
func (rootOnlyRun) CascadingPostRun(context.Context, *Context) {}
func (h rootOnlyRun) Run(_ context.Context, rtx *Context)      { h.capture(rtx) }
