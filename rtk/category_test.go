package rtk

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini"
)

// rtk's parse/bind failures (usageError) carry the usage category, so a single
// rotini.CategoryOf call in an OnError funnel classifies them as the end-user's fault.
func TestUsageError_categorizedAsUsage(t *testing.T) {
	rtx := rotini.NewContextFor(rotini.Definition{Name: "app", Handler: "App"}, nil)

	// A non-pointer out is the simplest parse-time usageError.
	err := NewParser().Parse(rtx, 42)
	if err == nil {
		t.Fatal("expected a usage error from Parse with a non-pointer out")
	}
	if got := rotini.CategoryOf(err); got != rotini.CategoryUsage {
		t.Errorf("CategoryOf(parse error) = %v, want usage", got)
	}
	if !errors.Is(err, rotini.ErrUsage) {
		t.Error("a parse usageError should match rotini.ErrUsage")
	}
	if errors.Is(err, rotini.ErrInternal) {
		t.Error("a usage error must not match rotini.ErrInternal")
	}
}
