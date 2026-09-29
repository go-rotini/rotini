package rotini

import (
	"errors"
	"strings"
	"testing"
)

// redactValue's own doc promises that "a secret flag/argument's value never appears in usage or
// validation errors". It was true for the paths that already had the input's definition in
// scope — enum violations, constraint violations, map-pair errors — and false for COERCION,
// which fails several frames below where the definition lives.
//
// A secret given a value of the wrong type therefore printed the value, through the default
// funnel, to stderr. These walk every way a declared input can reject a value.

const secretValue = "hunter2-DO-NOT-PRINT"

func secretFlagDef(typ string, enum ...string) Definition {
	return Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: typ, Secret: true, Enum: enum}}}
}

type secFlags struct {
	Token string `rotini:"token"`
}
type secIntFlags struct {
	Token int `rotini:"token"`
}
type secCmd struct {
	Flags     secFlags
	Arguments struct{}
}
type secIntCmd struct {
	Flags     secIntFlags
	Arguments struct{}
}
type secInputs struct{ App secCmd }
type secIntInputs struct{ App secIntCmd }

func mustNotLeak(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a rejection, got none", what)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Errorf("%s LEAKS the secret:\n  %v", what, err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("%s does not mark the value redacted, so the message is unhelpful:\n  %v", what, err)
	}
}

// TestSecret_coercionFailureIsRedacted is the leak that was there: a secret typed wrong.
func TestSecret_coercionFailureIsRedacted(t *testing.T) {
	_, err := Collect[secIntInputs](NewContextFor(secretFlagDef("int"), []string{"--token", secretValue}))
	mustNotLeak(t, "a secret flag coerced to int", err)
}

// TestSecret_argumentCoercionIsRedacted: positionals take the same path, and bindArgs had no
// access to the ArgDefs at all until this was fixed.
func TestSecret_argumentCoercionIsRedacted(t *testing.T) {
	def := Definition{Name: "app", Handler: "App",
		Arguments: []ArgDef{{Name: "tok", Type: "int", Secret: true}}}
	type args struct {
		Tok int `rotini:"tok"`
	}
	type cmd struct {
		Flags     struct{}
		Arguments args
	}
	type in struct{ App cmd }

	_, err := Collect[in](NewContextFor(def, []string{secretValue}))
	mustNotLeak(t, "a secret argument coerced to int", err)
}

// TestSecret_enumViolationIsRedacted covers the path that was already correct, so a later change
// cannot quietly regress it while fixing something else.
func TestSecret_enumViolationIsRedacted(t *testing.T) {
	_, err := Collect[secInputs](NewContextFor(secretFlagDef("string", "alpha", "beta"), []string{"--token", secretValue}))
	mustNotLeak(t, "a secret flag failing its enum", err)
}

// TestSecret_nonSecretValuesStillAppear is the other half: redaction must not swallow the
// information an ordinary user needs to fix their command line.
func TestSecret_nonSecretValuesStillAppear(t *testing.T) {
	def := Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int"}}}
	type f struct {
		Count int `rotini:"count"`
	}
	type c struct {
		Flags     f
		Arguments struct{}
	}
	type in struct{ App c }

	_, err := Collect[in](NewContextFor(def, []string{"--count", "twelve"}))
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), "twelve") {
		t.Errorf("a NON-secret value was hidden, which costs the user the diagnosis:\n  %v", err)
	}
}

// TestSecret_coerceErrorUnwrapsToItsCause keeps the typed error honest: wrapping the decoder's
// error was how the TextUnmarshaler path reported detail, and that must survive.
func TestSecret_coerceErrorUnwrapsToItsCause(t *testing.T) {
	cause := errors.New("underlying decoder said no")
	ce := &coerceError{Value: "x", TypeName: "widget", Cause: cause}

	if !errors.Is(ce, cause) {
		t.Error("coerceError does not unwrap to its cause")
	}
	if !strings.Contains(ce.Error(), "underlying decoder said no") {
		t.Errorf("the cause is not rendered: %q", ce.Error())
	}
	if !strings.Contains(ce.render(true), "[redacted]") || strings.Contains(ce.render(true), `"x"`) {
		t.Errorf("redacted rendering still shows the value: %q", ce.render(true))
	}
}

// TestCategory_severityOrdering pins the ordering CategoryOf's doc tells funnels to rely on when
// summarizing a run: a plain comparison must keep the worst category.
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

// TestCategory_usageWinsWithinOneError documents the other half: for a SINGLE value carrying
// both sentinels, usage wins. It is pinned so the tie-break cannot change silently.
func TestCategory_usageWinsWithinOneError(t *testing.T) {
	both := errors.Join(UsageError(errors.New("bad input")), InternalError(errors.New("a bug")))
	if got := CategoryOf(both); got != CategoryUsage {
		t.Errorf("CategoryOf(join(usage, internal)) = %v, want usage", got)
	}
	// And the reverse order gives the same answer — the tie-break is the test order, not the
	// argument order, which is what makes it predictable.
	flipped := errors.Join(InternalError(errors.New("a bug")), UsageError(errors.New("bad input")))
	if got := CategoryOf(flipped); got != CategoryUsage {
		t.Errorf("CategoryOf(join(internal, usage)) = %v, want usage", got)
	}
}
