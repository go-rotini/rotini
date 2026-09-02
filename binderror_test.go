package rotini

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
)

// beEnv is a minimal env channel: a bool that "junk" cannot coerce into, and a
// secret int whose bad value must never reach a BindError message.
type beEnv struct {
	Loud  bool `rotini:"loud" recon:"loud"`
	Token int  `rotini:"token" recon:"token,secret"`
}
type beCmdInputs struct {
	Flags     struct{}
	Arguments struct{}
	Env       beEnv
}
type beInputs struct {
	App beCmdInputs
}

func beBind(t *testing.T) error {
	t.Helper()
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in beInputs
	return NewBinder(BindMeta{}).Bind(rtx, &in)
}

// TestBindError_envCoercion_isCleanUsage is EH4's headline: the famous
// "recon: coerce …: string → bool" leak becomes a typed, categorized,
// non-leaky *BindError — while the recon cause stays reachable via errors.As.
func TestBindError_envCoercion_isCleanUsage(t *testing.T) {
	t.Setenv("LOUD", "junk") // not a bool
	err := beBind(t)
	if err == nil {
		t.Fatal("Bind = nil, want a coercion error for LOUD=junk")
	}

	// Typed + structured.
	var be *BindError
	if !errors.As(err, &be) {
		t.Fatalf("err is not a *BindError: %T (%v)", err, err)
	}
	if be.Channel != channelEnv || be.Input != "loud" {
		t.Errorf("BindError = {Channel:%q Input:%q}, want {env loud}", be.Channel, be.Input)
	}

	// Categorized as usage (errors.Is AND CategoryOf), not internal.
	if !errors.Is(err, ErrUsage) || errors.Is(err, ErrInternal) {
		t.Errorf("Is(ErrUsage)=%v Is(ErrInternal)=%v, want true/false", errors.Is(err, ErrUsage), errors.Is(err, ErrInternal))
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}

	// Non-leaky message: names the input in plain language, never echoes recon
	// internals or the bad value.
	msg := err.Error()
	if !strings.Contains(msg, "environment variable") || !strings.Contains(msg, `"loud"`) || !strings.Contains(msg, "expected") {
		t.Errorf("message = %q, want a clean environment-variable phrasing", msg)
	}
	if strings.Contains(msg, "recon") || strings.Contains(msg, "junk") {
		t.Errorf("message = %q, leaks recon text or the bad value", msg)
	}

	// The recon cause is still reachable for a handler that wants the detail.
	var ce *recon.CoercionError
	if !errors.As(err, &ce) {
		t.Error("errors.As could not reach the recon *CoercionError cause")
	} else if ce.Path.String() != "loud" {
		t.Errorf("CoercionError.Path = %q, want loud", ce.Path.String())
	}
}

// TestBindError_secretNeverLeaks: a coercion failure on a secret-tagged input
// must not put the offending value in the message.
func TestBindError_secretNeverLeaks(t *testing.T) {
	const secret = "sk_live_not_a_number"
	t.Setenv("TOKEN", secret) // not an int, and tagged secret
	err := beBind(t)
	if err == nil {
		t.Fatal("Bind = nil, want a coercion error for the secret TOKEN")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("message = %q, leaks the secret value", err.Error())
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}
}

// TestBindError_typeContract pins the type's category + unwrap behavior directly,
// independent of any channel: Error is the clean message, the category sentinel
// and the cause are both reachable.
func TestBindError_typeContract(t *testing.T) {
	boom := errors.New("low-level cause")

	usage := usageBind(channelConfig, "api.token", "config key \"api.token\" is required", boom)
	if usage.Error() != `config key "api.token" is required` {
		t.Errorf("Error() = %q, want the clean message", usage.Error())
	}
	if CategoryOf(usage) != CategoryUsage || !errors.Is(usage, boom) {
		t.Errorf("usageBind: CategoryOf=%v Is(boom)=%v, want usage/true", CategoryOf(usage), errors.Is(usage, boom))
	}

	internal := internalBind(channelConfig, "", "could not build the configuration registry", boom)
	if CategoryOf(internal) != CategoryInternal || !errors.Is(internal, boom) {
		t.Errorf("internalBind: CategoryOf=%v Is(boom)=%v, want internal/true", CategoryOf(internal), errors.Is(internal, boom))
	}

	// A nil cause is fine — the sentinel is still reachable.
	noCause := usageBind(channelStdin, "", "required stdin payload is empty", nil)
	if !errors.Is(noCause, ErrUsage) {
		t.Error("a nil-cause usage BindError must still match ErrUsage")
	}
}
