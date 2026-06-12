package rotini

import (
	"errors"
	"testing"
)

// rtk's parse/bind failures (usageError) carry the usage category, so a single
// CategoryOf call in an ErrorFn funnel classifies them as the end-user's fault.
func TestUsageError_categorizedAsUsage(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	// A non-pointer out is the simplest parse-time usageError.
	err := NewParser().Parse(rtx, 42)
	if err == nil {
		t.Fatal("expected a usage error from Parse with a non-pointer out")
	}
	if got := CategoryOf(err); got != CategoryUsage {
		t.Errorf("CategoryOf(parse error) = %v, want usage", got)
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("a parse usageError should match ErrUsage")
	}
	if errors.Is(err, ErrInternal) {
		t.Error("a usage error must not match ErrInternal")
	}
}
