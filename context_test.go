package rotini

import (
	"bytes"
	"errors"
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
