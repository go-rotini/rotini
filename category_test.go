package rotini

import (
	"errors"
	"fmt"
	"testing"
)

func TestCategoryOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Category
	}{
		{"nil", nil, CategoryNone},
		{"plain", errors.New("plain"), CategoryNone},
		{"usage", UsageError(errors.New("bad input")), CategoryUsage},
		{"internal", InternalError(errors.New("boom")), CategoryInternal},
		{"service error is internal", &ServiceError{Key: "parser"}, CategoryInternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CategoryOf(c.err); got != c.want {
				t.Errorf("CategoryOf = %v, want %v", got, c.want)
			}
		})
	}
}

// The category tag must not change the message, yet must match its sentinel and let
// errors.Is see through to the original error.
func TestUsageError_cleanMessageAndMatches(t *testing.T) {
	inner := errors.New("a widget id is required")
	err := UsageError(inner)

	if err.Error() != "a widget id is required" {
		t.Errorf("message was altered by the tag: %q", err.Error())
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("UsageError should match ErrUsage")
	}
	if !errors.Is(err, inner) {
		t.Error("UsageError should still match the wrapped error")
	}
	if errors.Is(err, ErrInternal) {
		t.Error("a usage error must not match ErrInternal")
	}
}

// The category survives further wrapping (errors.Is walks the chain), so a funnel can
// classify an error a handler annotated on the way up.
func TestCategory_survivesWrapping(t *testing.T) {
	err := fmt.Errorf("while creating widget: %w", UsageError(errors.New("bad id")))
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("category lost through wrapping: %v", CategoryOf(err))
	}
}

func TestCategory_nilConstructors(t *testing.T) {
	if UsageError(nil) != nil {
		t.Error("UsageError(nil) should be nil")
	}
	if InternalError(nil) != nil {
		t.Error("InternalError(nil) should be nil")
	}
}

// ServiceError gains the internal category without losing its existing identity: it still
// matches ErrServiceNotFound and recovers via errors.As.
func TestServiceError_backwardCompatible(t *testing.T) {
	err := error(&ServiceError{Key: "parser"})
	if !errors.Is(err, ErrServiceNotFound) {
		t.Error("ServiceError should still match ErrServiceNotFound")
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Key != "parser" {
		t.Errorf("errors.As should still recover the key, got %+v", se)
	}
}

func TestCategory_String(t *testing.T) {
	for c, want := range map[Category]string{
		CategoryNone:     "none",
		CategoryUsage:    "usage",
		CategoryInternal: "internal",
	} {
		if c.String() != want {
			t.Errorf("%d.String() = %q, want %q", c, c.String(), want)
		}
	}
}
