package rotini

import (
	"errors"
	"fmt"
	"testing"
)

func TestCompositionVersionError(t *testing.T) {
	ve := &CompositionVersionError{
		Arm:     CompositionSpecArm,
		Subject: "../deploy/.rotini.spec.yaml",
		Want:    "1.2.3",
		Got:     "1.2.0",
		Msg:     "composed spec targets 1.2.0 but this rotini is 1.2.3",
	}

	// Error is the carried message verbatim.
	if ve.Error() != ve.Msg {
		t.Errorf("Error() = %q, want %q", ve.Error(), ve.Msg)
	}

	// Unwraps to ErrInternal → categorized internal (a build/wiring concern, not user input).
	if !errors.Is(ve, ErrInternal) {
		t.Error("want errors.Is(ve, ErrInternal)")
	}
	if got := CategoryOf(ve); got != CategoryInternal {
		t.Errorf("CategoryOf = %v, want CategoryInternal", got)
	}

	// errors.As recovers the typed value through a wrap, preserving the arm + versions.
	wrapped := fmt.Errorf("generate failed: %w", ve)
	var got *CompositionVersionError
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not recover *CompositionVersionError")
	}
	if got.Arm != CompositionSpecArm || got.Want != "1.2.3" || got.Got != "1.2.0" {
		t.Errorf("recovered = %+v", got)
	}
}
