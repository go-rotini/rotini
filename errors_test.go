package rotini

import (
	"context"
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
		{"service error is internal", &DependencyError{Name: "parser"}, CategoryInternal},
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

	typed := &ArgvError{Field: "x", Reason: "y"}
	for name, tagged := range map[string]error{"UsageError": UsageError(typed), "InternalError": InternalError(typed)} {
		if tagged.Error() != typed.Error() {
			t.Errorf("%s altered the message: %q", name, tagged.Error())
		}
		if got, ok := errors.AsType[*ArgvError](tagged); !ok || got != typed {
			t.Errorf("%s: errors.As does not reach the tagged error", name)
		}
	}
}

// The category survives further wrapping (errors.Is walks the chain), so a reporter can
// classify an error a handler annotated on the way up.
func TestCategory_survivesWrapping(t *testing.T) {
	err := fmt.Errorf("while creating widget: %w", UsageError(errors.New("bad id")))
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("category lost through wrapping: %v", CategoryOf(err))
	}
}

// Command and Handler name the command only for a handler mismatch; every other wiring error
// leaves them empty. Each one is internal.
func TestWiringError_fields(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}
	quiet := func(p *Program) *Program {
		return p.WithReporter(func(context.Context, *Context, Outcome) {}).WithExit(func(int) {})
	}
	missing := func(string) (Handler, bool) { return nil, false }
	notHandler := func(string) (Handler, bool) { return nil, true }
	_, nilLookup := quiet(NewProgramFunc(def, nil)).Run(nil)
	_, nilHandlers := quiet(NewProgram(def, nil)).Run(nil)
	_, noMethod := quiet(NewProgramFunc(def, missing)).Run(nil)
	_, nilHandler := quiet(NewProgramFunc(def, notHandler)).Run(nil)
	_, noScripts := NewContextFor(def, nil).CompletionScript("bash")

	cases := []struct {
		name             string
		err              error
		command, handler string
	}{
		{"nil lookup", nilLookup, "", ""},
		{"nil handlers", nilHandlers, "", ""},
		{"no completion scripts", noScripts, "", ""},
		{"missing method", noMethod, "app", "App"},
		{"not a Handler", nilHandler, "app", "App"},
	}
	for _, c := range cases {
		we, ok := errors.AsType[*WiringError](c.err)
		if !ok {
			t.Errorf("%s: err = %v, want a *WiringError", c.name, c.err)
			continue
		}
		if we.Command != c.command || we.Handler != c.handler || we.Feature != "" {
			t.Errorf("%s: Command=%q Handler=%q Feature=%q, want %q %q \"\"",
				c.name, we.Command, we.Handler, we.Feature, c.command, c.handler)
		}
		if CategoryOf(c.err) != CategoryInternal || we.Error() != we.Msg {
			t.Errorf("%s: category %v, Error() %q vs Msg %q", c.name, CategoryOf(c.err), we.Error(), we.Msg)
		}
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

// DependencyError is internal-category while keeping its own identity: it still
// matches ErrDependencyNotFound and recovers via errors.As.
func TestDependencyError_matchesSentinel(t *testing.T) {
	err := error(&DependencyError{Name: "parser"})
	if !errors.Is(err, ErrDependencyNotFound) {
		t.Error("DependencyError should still match ErrDependencyNotFound")
	}
	var se *DependencyError
	if !errors.As(err, &se) || se.Name != "parser" {
		t.Errorf("errors.As should still recover the key, got %+v", se)
	}
}

func TestCategory_String(t *testing.T) {
	for c, want := range map[Category]string{
		CategoryNone:     "none",
		CategoryUsage:    "usage",
		CategoryInternal: "internal",
		Category(99):     "none",
	} {
		if c.String() != want {
			t.Errorf("%d.String() = %q, want %q", c, c.String(), want)
		}
	}
}

// TestCategory_severityOrdering pins none < usage < internal, so a plain comparison keeps the
// worst category.
func TestCategory_severityOrdering(t *testing.T) {
	if !(CategoryNone < CategoryUsage && CategoryUsage < CategoryInternal) {
		t.Fatal("the categories are no longer ordered none < usage < internal")
	}

	worst := CategoryNone
	for _, err := range []error{
		errors.New("unclassified"),
		UsageError(errors.New("bad input")),
		InternalError(errors.New("a bug")),
	} {
		if c := CategoryOf(err); c > worst {
			worst = c
		}
	}
	if worst != CategoryInternal {
		t.Errorf("worst-of = %v, want internal — a bug must not be summarized as the user's fault", worst)
	}
}

// TestCategory_usageWinsWithinOneError pins that a single error carrying both sentinels reports
// usage, regardless of join order.
func TestCategory_usageWinsWithinOneError(t *testing.T) {
	both := errors.Join(UsageError(errors.New("bad input")), InternalError(errors.New("a bug")))
	if got := CategoryOf(both); got != CategoryUsage {
		t.Errorf("CategoryOf(join(usage, internal)) = %v, want usage", got)
	}
	flipped := errors.Join(InternalError(errors.New("a bug")), UsageError(errors.New("bad input")))
	if got := CategoryOf(flipped); got != CategoryUsage {
		t.Errorf("CategoryOf(join(internal, usage)) = %v, want usage", got)
	}
}
