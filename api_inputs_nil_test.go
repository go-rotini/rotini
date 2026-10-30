package rotini

import (
	"errors"
	"testing"
)

// TestNilContextContract pins the Context doc's nil-receiver promise: the With setters do
// nothing and return nil, the inputs methods return an error, and Deprecations returns none.
func TestNilContextContract(t *testing.T) {
	var rtx *Context
	setters := map[string]*Context{
		"WithInputSettings":    rtx.WithInputSettings(InputSettings{}),
		"WithInputReader":      rtx.WithInputReader(nil),
		"WithVersion":          rtx.WithVersion("1"),
		"WithHelp":             rtx.WithHelp(nil),
		"WithCompletionScript": rtx.WithCompletionScript(nil),
		"WithParser":           rtx.WithParser(nil),
		"WithStdin":            rtx.WithStdin(nil),
		"WithStdout":           rtx.WithStdout(nil),
		"WithStderr":           rtx.WithStderr(nil),
		"WithOutputChecks":     rtx.WithOutputChecks(true),
		"WithEnviron":          rtx.WithEnviron(nil),
		"WithDir":              rtx.WithDir("x"),
		"WithClock":            rtx.WithClock(nil),
	}
	for name, got := range setters {
		if got != nil {
			t.Errorf("nil.%s returned %p, want nil", name, got)
		}
	}

	errs := map[string]func() error{
		"Inputs":           func() error { _, err := rtx.Inputs[ciInputs](); return err },
		"InputsWithReport": func() error { _, _, err := rtx.InputsWithReport[ciInputs](); return err },
		"ArgvInputs":       func() error { _, err := rtx.ArgvInputs[ciInputs](); return err },
		"EnvInputs":        func() error { _, err := rtx.EnvInputs[ciInputs](); return err },
		"FileInputs":       func() error { _, err := rtx.FileInputs[ciInputs](); return err },
		"StdinInputs":      func() error { _, err := rtx.StdinInputs[ciInputs](); return err },
		"DefaultInputs":    func() error { _, err := rtx.DefaultInputs[ciInputs](); return err },
		"CheckInputs":      func() error { return rtx.CheckInputs(ciInputs{}, nil) },
		"PartialInputs":    func() error { _, _, err := rtx.PartialInputs[ciInputs](); return err },
	}
	for name, call := range errs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("nil.%s panicked: %v", name, r)
				}
			}()
			if err := call(); err == nil {
				t.Errorf("nil.%s returned no error", name)
			}
		}()
	}
	if d := Deprecations(nil); d != nil {
		t.Errorf("Deprecations(nil) = %v, want none", d)
	}
	if d := Deprecations(&Context{}); d != nil {
		t.Errorf("Deprecations(no command) = %v, want none", d)
	}
}

// TestDependencyContract pins the Dependency docs: GetDependency never panics, a different
// type is a miss, and MustGetDependency's panic is a *DependencyError in CategoryInternal that
// unwraps to ErrDependencyNotFound.
func TestDependencyContract(t *testing.T) {
	rtx := NewContextFor(ciDef(), nil)
	num := NewDependency[int]("n")
	str := NewDependency[string]("n")
	rtx.SetDependency(num, 3)
	if v, ok := rtx.GetDependency(num); !ok || v != 3 {
		t.Fatalf("GetDependency = %v, %v", v, ok)
	}
	if _, ok := rtx.GetDependency(str); ok {
		t.Fatal("GetDependency under the wrong type reported a hit")
	}
	rtx.SetDependencyIfAbsent(num, 4)
	if v, _ := rtx.GetDependency(num); v != 3 {
		t.Fatalf("SetDependencyIfAbsent replaced a registered value: %v", v)
	}
	mustPanic := func(f func()) (de *DependencyError) {
		defer func() {
			err, _ := recover().(error)
			if !errors.As(err, &de) || !errors.Is(err, ErrDependencyNotFound) || CategoryOf(err) != CategoryInternal {
				t.Fatalf("panic %v: want a *DependencyError in CategoryInternal wrapping ErrDependencyNotFound", err)
			}
		}()
		f()
		return nil
	}
	if de := mustPanic(func() { rtx.MustGetDependency(str) }); de.Name != "n" {
		t.Errorf("Name = %q", de.Name)
	}
	mustPanic(func() { rtx.MustGetDependency(NewDependency[int]("missing")) })
	if NewDependency[int]("x").String() != "x" || (Dependency[int]{}).Name() != "" {
		t.Error("Name/String")
	}
}

// TestContextAccessorCopies pins that Environ and CommandChain return copies.
func TestContextAccessorCopies(t *testing.T) {
	rtx := NewContextFor(ciDef(), nil).WithEnviron([]string{"A=1"})
	env := rtx.Environ()
	env[0] = "A=2"
	if v, _ := rtx.LookupEnv("A"); v != "1" {
		t.Errorf("Environ returned the live slice: A=%q", v)
	}
	chain := rtx.CommandChain()
	chain[0].Name = "changed"
	if rtx.CommandChain()[0].Name != "app" {
		t.Error("CommandChain returned the live slice")
	}
	if rtx.Parser() == nil || rtx.WithParser(nil).Parser() == nil {
		t.Error("Parser returned nil")
	}
	if rtx.Version() != "" || rtx.Help() != "" {
		t.Error("Version/Help defaults are not empty")
	}
	if now := rtx.Now(); !rtx.Now().Equal(now) {
		t.Error("Now is not stable")
	}
}
