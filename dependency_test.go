package rotini

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// A typed Dependency exists so the stored name and the type assertion cannot drift, and so a
// handler needs neither. These pin both halves.

type demoStore interface{ All() []string }

type memDemoStore struct{ items []string }

func (m memDemoStore) All() []string { return m.items }

var demoStoreDep = NewDependency[demoStore]("demo-store")

func TestDependency_programWideAndGet(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		store := rtx.MustGetDependency(demoStoreDep)
		if got := store.All(); len(got) != 2 {
			t.Errorf("store.All() = %v, want 2 items", got)
		}
		if _, ok := rtx.GetDependency(demoStoreDep); !ok {
			t.Error("GetDependency reported the dependency as missing")
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	p.WithDependency(demoStoreDep, demoStore(memDemoStore{items: []string{"a", "b"}}))

	if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
}

// Two handles with the same name and type are the same dependency: a handle built at run time
// from a computed name reaches what main.go registered.
func TestDependency_handlesShareByName(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		if _, ok := rtx.GetDependency(NewDependency[demoStore](demoStoreDep.Name())); !ok {
			t.Error("a handle rebuilt from the name did not reach the registered value")
		}
		if v, ok := rtx.GetDependency(NewDependency[any](demoStoreDep.Name())); !ok || v == nil {
			t.Error("a Dependency[any] handle did not reach the registered value")
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	p.WithDependency(demoStoreDep, demoStore(memDemoStore{}))
	p.Run([]string{"run", "x"})
}

// A value registered through a Dependency[any] keeps its precise type: the storage is the
// same, so a typed handle reads it back.
func TestDependency_anyHandleKeepsThePreciseType(t *testing.T) {
	rtx := newContext()
	rtx.SetDependency(NewDependency[any]("buf"), any(&bytes.Buffer{}))
	if _, ok := rtx.GetDependency(NewDependency[*bytes.Buffer]("buf")); !ok {
		t.Error("a value set through Dependency[any] is not readable with its precise type")
	}
	if _, ok := rtx.GetDependency(NewDependency[*int]("buf")); ok {
		t.Error("a value of another type read back as *int")
	}
}

// A missing dependency routes to the reporter as a fault rather than a nil dereference.
func TestDependency_missingReachesTheReporter(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_ = rtx.MustGetDependency(demoStoreDep) // never registered
	}}
	p, _, errb := newTestProgram(h, nil)

	code, _ := p.Run([]string{"run", "x"})
	if code == 0 {
		t.Error("a missing dependency exited 0")
	}
	if !strings.Contains(errb.String(), demoStoreDep.Name()) {
		t.Errorf("the failure does not name the missing dependency:\n%s", errb.String())
	}
}

// SetDependency scopes a value to one run, for a dependency a hook computes per run.
func TestDependency_setIsPerRun(t *testing.T) {
	var seen []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_, ok := rtx.GetDependency(demoStoreDep)
		seen = append(seen, ok)
		rtx.SetDependency(demoStoreDep, demoStore(memDemoStore{}))
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if len(seen) != 2 || seen[0] || seen[1] {
		t.Errorf("SetDependency visibility = %v, want [false false] — a per-run value must not leak forward", seen)
	}
}

// SetDependencyIfAbsent keeps what is already there (a test's double) and fills what is not.
func TestDependency_setIfAbsentKeepsTheFirst(t *testing.T) {
	buf := NewDependency[*bytes.Buffer]("buf")
	injected, def := &bytes.Buffer{}, &bytes.Buffer{}

	rtx := newContext()
	rtx.SetDependency(buf, injected)
	rtx.SetDependencyIfAbsent(buf, def)
	if rtx.MustGetDependency(buf) != injected {
		t.Error("SetDependencyIfAbsent overwrote an existing value")
	}

	other := NewDependency[*bytes.Buffer]("other")
	rtx.SetDependencyIfAbsent(other, def)
	if rtx.MustGetDependency(other) != def {
		t.Error("SetDependencyIfAbsent did not set a missing value")
	}
}

// MustGetDependency's panic says which of the two misses it is: nothing registered, or
// something of another type.
func TestDependencyError_saysWhichMissItIs(t *testing.T) {
	n := NewDependency[int]("n")
	miss := func(rtx *Context) (msg string) {
		defer func() { msg = recover().(*DependencyError).Error() }()
		rtx.MustGetDependency(n)
		return ""
	}
	if got := miss(newContext()); got != `rotini: no dependency registered as "n"` {
		t.Errorf("missing = %q", got)
	}
	rtx := newContext()
	rtx.SetDependency(NewDependency[any]("n"), any("seven"))
	if got := miss(rtx); got != `rotini: the dependency "n" is a string, not the int requested` {
		t.Errorf("wrong type = %q", got)
	}
}

// The panic unwraps to the sentinel and recovers by name.
func TestDependencyError_isMatchable(t *testing.T) {
	defer func() {
		err, _ := recover().(error)
		if !errors.Is(err, ErrDependencyNotFound) {
			t.Errorf("panic = %v, want it to wrap ErrDependencyNotFound", err)
		}
		var de *DependencyError
		if !errors.As(err, &de) || de.Name != "missing" {
			t.Errorf("errors.As = %v, want a *DependencyError naming %q", de, "missing")
		}
	}()
	_ = newContext().MustGetDependency(NewDependency[*bytes.Buffer]("missing"))
}

func TestDependency_nameAndString(t *testing.T) {
	if demoStoreDep.Name() != "demo-store" || demoStoreDep.String() != "demo-store" {
		t.Errorf("Name/String = %q/%q, want demo-store", demoStoreDep.Name(), demoStoreDep.String())
	}
}

// A dependency declared over an INTERFACE accepts a concrete value once the type is named —
// the shape a real CLI has, where a constructor returns a concrete type. Go infers T from both
// arguments, so without the type argument this would not compile.
func TestDependency_acceptsAssignableConcreteType(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	p.WithDependency[demoStore](demoStoreDep, memDemoStore{items: []string{"x"}}) // concrete, not demoStore
}

// TestWithDependency_chains: the option form groups several typed registrations in one
// With call without ending the chain.
func TestWithDependency_chains(t *testing.T) {
	type store struct{ name string }
	type client struct{ id int }

	storeDep := NewDependency[*store]("p2.store")
	clientDep := NewDependency[*client]("p2.client")

	var got string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		got = rtx.MustGetDependency(storeDep).name + "/" + strconv.Itoa(rtx.MustGetDependency(clientDep).id)
	}}
	p, _, _ := newTestProgram(h, nil)

	code, err := p.
		With(
			WithDependency(storeDep, &store{name: "disk"}),
			WithDependency(clientDep, &client{id: 7}),
		).
		WithVersion("9.9.9").
		Run([]string{"run", "x"})
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	if got != "disk/7" {
		t.Errorf("dependencies seen by the handler = %q, want %q", got, "disk/7")
	}
}

// TestWithDependency_lastWins pins the ordering rule: Options apply left to right, so a later
// registration under the same name replaces an earlier one.
func TestWithDependency_lastWins(t *testing.T) {
	dep := NewDependency[string]("p2.order")

	var got string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.MustGetDependency(dep) }}
	p, _, _ := newTestProgram(h, nil)

	if code, err := p.With(WithDependency(dep, "first"), WithDependency(dep, "second")).Run([]string{"run", "x"}); err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v)", code, err)
	}
	if got != "second" {
		t.Errorf("got %q, want the later Option to win", got)
	}
}

// TestWith_toleratesNothing covers the degenerate OPTION inputs, so a caller assembling an
// option slice conditionally does not have to guard every element.
//
// A nil *Program is deliberately NOT in that set: see TestProgram_nilReceiverPanicsAtTheCall.
func TestWith_toleratesNothing(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	if p.With() != p {
		t.Error("With() with no options must return the same program")
	}
	if p.With(nil) != p {
		t.Error("With(nil) must skip the nil option and return the same program")
	}
	if p.With(nil, WithDependency(NewDependency[int]("p2.mixed"), 1), nil) != p {
		t.Error("With must skip nil options interleaved with real ones")
	}
}

// TestProgram_nilReceiverPanicsAtTheCall pins the contract the whole surface follows: a nil
// *Program is a caller bug, and every method dereferences rather than checking.
//
// Two of these methods used to return the nil receiver instead, which is worse: the nil then
// travels down the chain and panics somewhere later, at a call that was not the mistake.
func TestProgram_nilReceiverPanicsAtTheCall(t *testing.T) {
	for name, call := range map[string]func(*Program){
		"With":              func(p *Program) { p.With(WithDependency(NewDependency[int]("x"), 1)) },
		"WithInputSettings": func(p *Program) { p.WithInputSettings(InputSettings{}) },
		"WithVersion":       func(p *Program) { p.WithVersion("1") },
		"WithDependency":    func(p *Program) { p.WithDependency(NewDependency[int]("x"), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic: the nil receiver was carried onward instead of failing here")
				}
			}()
			call(nil)
		})
	}
}

// TestWith_carriesAnArbitraryOption proves Option is not for dependencies only: it is an
// ordinary function over the program, so a caller can bundle any configuration into one value.
func TestWith_carriesAnArbitraryOption(t *testing.T) {
	var out bytes.Buffer
	quiet := Option(func(p *Program) { p.WithStdout(&out).WithoutSignalHandling() })

	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { fmt.Fprint(rtx.Stdout, "hello") }}
	p, _, _ := newTestProgram(h, nil)

	if code, err := p.With(quiet).Run([]string{"run", "x"}); err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v)", code, err)
	}
	if out.String() != "hello" {
		t.Errorf("stdout = %q, want the option's writer to have been installed", out.String())
	}
}
