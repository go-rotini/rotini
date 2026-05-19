package rtk_test

import (
	"errors"
	"os"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Default OS
// =============================================================================

func TestOS_NewOS_lookupReadsProcessEnv(t *testing.T) {
	// Not parallel — exercises os.Setenv, which is process-global.
	const key, value = "RTK_TEST_OS_LOOKUP", "hello"
	t.Setenv(key, value)

	rios := rtk.NewOS()
	got, ok := rios.Lookup(key)
	if !ok {
		t.Fatalf("Lookup(%q): not found", key)
	}
	if got != value {
		t.Errorf("Lookup(%q): got %q, want %q", key, got, value)
	}
}

func TestOS_NewOS_lookupMissingKey(t *testing.T) {
	t.Parallel()
	rios := rtk.NewOS()
	// Use an unlikely key name so the absence is reliable.
	if _, ok := rios.Lookup("RTK_UNLIKELY_KEY_42c79a"); ok {
		t.Errorf("Lookup of unlikely key returned ok=true")
	}
}

// TestOS_SatisfiesEnvLookup verifies the default OS satisfies the parser's
// EnvLookup interface so it can be threaded into rtk.Inputs.Env directly.
func TestOS_SatisfiesEnvLookup(t *testing.T) {
	t.Parallel()
	var _ rtk.EnvLookup = rtk.NewOS()
}

// =============================================================================
// Exit + EarlyExit recovery
// =============================================================================

func TestOS_Exit_PanicsWithEarlyExit(t *testing.T) {
	t.Parallel()
	rios := rtk.NewOS()

	caught := func() (recovered any) {
		defer func() { recovered = recover() }()
		rios.Exit(42)
		return nil
	}()

	if caught == nil {
		t.Fatal("Exit did not panic")
	}
	ee, ok := caught.(*rtk.EarlyExit)
	if !ok {
		t.Fatalf("panic value: got %T, want *rtk.EarlyExit", caught)
	}
	if ee.Code != 42 {
		t.Errorf("EarlyExit.Code: got %d, want 42", ee.Code)
	}
}

func TestEarlyExit_ErrorMessage(t *testing.T) {
	t.Parallel()
	ee := &rtk.EarlyExit{Code: 7}
	if ee.Error() == "" {
		t.Error("EarlyExit.Error() returned empty string")
	}
}

func TestEarlyExit_ErrorsAs(t *testing.T) {
	t.Parallel()
	// A lifecycle layer that returns the recovered EarlyExit through
	// errors.As should be able to extract it from a wrapped error.
	src := &rtk.EarlyExit{Code: 9}
	var target *rtk.EarlyExit
	if !errors.As(src, &target) {
		t.Fatal("errors.As failed to extract *EarlyExit")
	}
	if target.Code != 9 {
		t.Errorf("extracted Code: got %d, want 9", target.Code)
	}
}

// =============================================================================
// Fake OS (test-side injection pattern)
// =============================================================================

// fakeOS captures Exit calls and stubs env lookups — the canonical pattern
// for testing handlers without actually exiting the process.
type fakeOS struct {
	env       map[string]string
	exitCodes []int
}

func (f *fakeOS) Lookup(key string) (string, bool) {
	v, ok := f.env[key]
	return v, ok
}

func (f *fakeOS) Exit(code int) {
	f.exitCodes = append(f.exitCodes, code)
}

func TestOS_FakeImplementsInterface(t *testing.T) {
	t.Parallel()
	var rios rtk.OS = &fakeOS{env: map[string]string{"K": "V"}}

	got, ok := rios.Lookup("K")
	if !ok || got != "V" {
		t.Errorf("Lookup: got (%q, %v), want (\"V\", true)", got, ok)
	}

	rios.Exit(3)
	f := rios.(*fakeOS)
	if len(f.exitCodes) != 1 || f.exitCodes[0] != 3 {
		t.Errorf("Exit: got %v, want [3]", f.exitCodes)
	}
}

// =============================================================================
// Registry binding
// =============================================================================

func TestOS_BoundInRegistry(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	want := rtk.NewOS()
	reg.Bind("os", want)
	got := rtk.Get[rtk.OS](reg, "os")
	if got == nil {
		t.Fatal("Get returned nil OS")
	}
	// Sanity: the retrieved OS reads from the same process env as the
	// freshly-bound one.
	if _, ok := got.Lookup("PATH"); !ok && os.Getenv("PATH") != "" {
		t.Errorf("retrieved OS could not look up PATH but env has it set")
	}
}
