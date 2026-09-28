package rotini

import (
	"fmt"
	"reflect"
	"testing"
)

// Collect and CollectP are two code paths to one answer: Collect goes through [Binder.Bind],
// CollectP acquires five layers separately and overlays them. TestCollect pins that they agree
// for one argv shape. These pin that they agree across the whole precedence matrix — and, which
// nothing covered, that they FAIL the same way.
//
// The failure half matters most. A handler reaches for CollectP when it wants provenance, often
// after starting with Collect; if the two disagreed on which inputs are legal, that swap would
// change behaviour while looking like it only added a Report.

func describeErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T: %s", err, err.Error())
}

func TestCollect_andCollectPAgreeAcrossThePrecedenceMatrix(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	withFiles := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	for _, c := range []struct {
		name string
		argv []string
		env  string // APP_COLOR; "" leaves it unset
		meta BindMeta
		want string // merged Color, "" when the case is an error
	}{
		{"argv beats everything", []string{"--color", "green"}, "teal", withFiles, "green"},
		{"env beats files and default", nil, "teal", withFiles, "teal"},
		{"files beat the default", nil, "", withFiles, "red"},
		{"the default when nothing supplies", nil, "", BindMeta{}, "blue"},
		{"an enum violation on argv", []string{"--color", "mauve"}, "", withFiles, ""},
		{"an enum violation from env", nil, "mauve", withFiles, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("APP_COLOR", c.env)
			}
			mk := func() *Context {
				rtx := NewContextFor(ovDef(), c.argv)
				rtx.WithBindMeta(c.meta)
				return rtx
			}

			viaCollect, errCollect := Collect[ovInputs](mk())
			viaCollectP, _, errCollectP := CollectP[ovInputs](mk())

			// The verdict must be identical: swapping Collect for CollectP to gain provenance
			// must not change which inputs are legal.
			if describeErr(errCollect) != describeErr(errCollectP) {
				t.Errorf("the two paths disagree on failure:\n  Collect  → %s\n  CollectP → %s",
					describeErr(errCollect), describeErr(errCollectP))
			}

			// On SUCCESS the values must be identical too.
			if errCollect == nil && !reflect.DeepEqual(viaCollect, viaCollectP) {
				t.Errorf("the two paths disagree on values:\n  Collect  → %+v\n  CollectP → %+v",
					viaCollect.App, viaCollectP.App)
			}

			// On FAILURE they deliberately differ, and the difference is pinned rather than
			// left to be rediscovered: Collect stops at the first argv problem, before the env
			// and config channels are read, so a config-supplied default (Retries, recon
			// default=3) is absent from its partial value and present in CollectP's.
			if errCollect != nil {
				if viaCollect.App.Config.Retries != 0 {
					t.Errorf("Collect's partial value gained a config default it should not have read: %d",
						viaCollect.App.Config.Retries)
				}
				if viaCollectP.App.Config.Retries != 3 {
					t.Errorf("CollectP's merged value lost the config default: %d", viaCollectP.App.Config.Retries)
				}
			}
			if c.want != "" {
				if errCollect != nil {
					t.Fatalf("Collect: %v", errCollect)
				}
				if got := viaCollect.App.Flags.Color; got != c.want {
					t.Errorf("Color = %q, want %q", got, c.want)
				}
			} else if errCollect == nil {
				t.Error("an enum violation was accepted")
			}
		})
	}
}

// TestCollect_isIdempotent: a handler that collects twice — or a helper that collects for
// itself — must get the same answer, since the Context is shared across a run's hooks.
func TestCollect_isIdempotent(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	rtx := NewContextFor(ovDef(), []string{"--color", "green"})
	rtx.WithBindMeta(BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	first, err1 := Collect[ovInputs](rtx)
	second, err2 := Collect[ovInputs](rtx)
	if err1 != nil || err2 != nil {
		t.Fatalf("Collect: %v / %v", err1, err2)
	}
	if first != second {
		t.Errorf("Collect is not idempotent on one Context:\n  first  = %+v\n  second = %+v", first.App, second.App)
	}
}

// TestReport_validateChecksWhatWasSupplied pins the rule Validate's doc now states, because the
// two halves look the same from the outside and only one of them fires on an absent field.
func TestReport_validateChecksWhatWasSupplied(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color"}, Type: "string", Default: "blue",
				Enum: []string{"red", "green", "blue"}},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Required: true,
				Enum: []string{"fast", "slow"}},
		},
	}
	type flags struct {
		Color string `rotini:"color"`
		Mode  string `rotini:"mode"`
	}
	type cmd struct {
		Flags     flags
		Arguments struct{}
	}
	type inputs struct{ App cmd }

	argvOnly, err := ParseArgv[inputs](NewContextFor(def, nil))
	if err != nil {
		t.Fatal(err)
	}

	// A PRESENCE rule fires on absence: --mode is required and nothing supplied it.
	merged, rep := OverlayInputsP(argvOnly)
	if e := rep.Validate(); e == nil {
		t.Error("a required input nobody supplied passed validation")
	}

	// A VALUE rule does not fire on absence: Color merged as "" — not in its enum — because no
	// layer claimed it. Dropping the defaults layer is what exposes this, which is why the doc
	// says to build custom precedence from all five channels.
	if merged.App.Flags.Color != "" {
		t.Fatalf("fixture no longer demonstrates the case: Color = %q", merged.App.Flags.Color)
	}
	for _, p := range rep.Fields() {
		if p == "App.Flags.Color" {
			t.Error("Color was reported as supplied by a layer, which it was not")
		}
	}

	// With the defaults layer present the field is supplied, and the enum applies again.
	defaults, err := Defaults[inputs](NewContextFor(def, nil))
	if err != nil {
		t.Fatal(err)
	}
	withDefaults, _ := OverlayInputsP(defaults, argvOnly)
	if withDefaults.App.Flags.Color != "blue" {
		t.Errorf("Color with defaults = %q, want the declared default", withDefaults.App.Flags.Color)
	}
}
