package rotini

import (
	"strings"
	"testing"
)

type argGapInputs struct {
	App struct {
		Arguments struct {
			A string `rotini:"a"`
			B string `rotini:"b" recon:"b" env:"APP_B"`
		}
	}
}

func argGapDef(bDefault string, bEnum ...string) Definition {
	return Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "a", Type: "string"},
		{Name: "b", Type: "string", Default: bDefault, Enum: bEnum},
	}}
}

// InputsWithReport places an argument's fallback or default by the same gap rule as Inputs:
// behind an argument with no value it is left out, and when placed it is checked, its error
// naming where the value came from.
func TestInputsWithReport_argumentGapRule(t *testing.T) {
	env := []string{"APP_B=fromenv"}
	for _, def := range []Definition{argGapDef(""), argGapDef("dflt")} {
		rtx := NewContextFor(def, nil).WithEnviron(env)
		in, err := rtx.Inputs[argGapInputs]()
		if err != nil || in.App.Arguments.B != "" {
			t.Fatalf("Inputs: B %q, err %v; want it left out", in.App.Arguments.B, err)
		}
		got, rep, err := rtx.InputsWithReport[argGapInputs]()
		if err != nil || got.App.Arguments.B != "" {
			t.Errorf("InputsWithReport: B %q, err %v; want it left out as Inputs does", got.App.Arguments.B, err)
		}
		if _, ok := rep.Winner("App.Arguments.B"); ok {
			t.Errorf("InputsWithReport: B has a winner")
		}
	}

	rtx := NewContextFor(argGapDef(""), []string{"first"}).WithEnviron(env)
	got, rep, err := rtx.InputsWithReport[argGapInputs]()
	if err != nil || got.App.Arguments.B != "fromenv" {
		t.Errorf("after <a>: B %q, err %v; want the fallback", got.App.Arguments.B, err)
	}
	if w, ok := rep.Winner("App.Arguments.B"); !ok || w.Layer != "env" {
		t.Errorf("after <a>: winner %+v, %v", w, ok)
	}

	def := argGapDef("", "x")
	env = []string{"APP_B=notallowed"}
	_, inputsErr := NewContextFor(def, []string{"first"}).WithEnviron(env).Inputs[argGapInputs]()
	_, _, reportErr := NewContextFor(def, []string{"first"}).WithEnviron(env).InputsWithReport[argGapInputs]()
	if inputsErr == nil || reportErr == nil || inputsErr.Error() != reportErr.Error() || !strings.Contains(reportErr.Error(), "APP_B") {
		t.Errorf("enum: Inputs %v, InputsWithReport %v; want the same error naming APP_B", inputsErr, reportErr)
	}
	if _, _, err := NewContextFor(def, nil).WithEnviron(env).InputsWithReport[argGapInputs](); err != nil {
		t.Errorf("enum behind a gap: %v, want the value left out unchecked, as Inputs does", err)
	}
}
