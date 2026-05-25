package rtk

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini"
)

func TestGet_typed(t *testing.T) {
	rtx := rotini.NewContext()
	rtx.Bind("parser", NewParser())

	if p, ok := Get[*Parser](rtx, "parser"); !ok || p == nil {
		t.Fatalf("Get[*Parser] = (%v, %v), want a parser", p, ok)
	}
	if _, ok := Get[*Parser](rtx, "missing"); ok {
		t.Error("Get of an unbound key should be ok=false")
	}
	if _, ok := Get[*IO](rtx, "parser"); ok {
		t.Error("Get of a wrong-typed binding should be ok=false")
	}
}

func TestMustGet_returnsBoundService(t *testing.T) {
	rtx := rotini.NewContext()
	rtx.Bind("parser", NewParser())
	if MustGet[*Parser](rtx, "parser") == nil {
		t.Fatal("MustGet returned nil for a bound service")
	}
}

func TestMustGet_panicsServiceError(t *testing.T) {
	rtx := rotini.NewContext() // nothing bound

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok {
			t.Fatalf("MustGet panicked with %v (%T), want an error", r, r)
		}
		if !errors.Is(err, rotini.ErrServiceNotFound) {
			t.Errorf("panic = %v, want it to wrap ErrServiceNotFound", err)
		}
		var se *rotini.ServiceError
		if !errors.As(err, &se) || se.Key != "missing" {
			t.Errorf("panic did not carry the key: %v", err)
		}
	}()

	_ = MustGet[*Parser](rtx, "missing") // panics → recovered above
}

func TestMustGet_panicsOnWrongType(t *testing.T) {
	rtx := rotini.NewContext()
	rtx.Bind("parser", NewParser())

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet of a wrong-typed binding should panic")
		}
	}()

	_ = MustGet[*IO](rtx, "parser") // bound, but not an *IO → panics
}
