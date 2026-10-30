package rotini

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func dashDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{{Name: "words", Type: "[]string", Variadic: true}},
	}
}

// TestDashIndexDocExamples pins the table in Context.DashIndex's doc.
func TestDashIndexDocExamples(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		n    int
		ok   bool
	}{
		{[]string{"--", "ls", "-la"}, 0, true},
		{[]string{"a", "--", "b"}, 1, true},
		{[]string{"ls"}, 0, false},
	} {
		n, ok := NewContextFor(dashDef(), tt.argv).DashIndex()
		if n != tt.n || ok != tt.ok {
			t.Errorf("DashIndex(%q) = %d, %v; want %d, %v", tt.argv, n, ok, tt.n, tt.ok)
		}
	}
}

// TestContextWithDirAbsolute pins "A relative dir is made absolute now" and that a nil
// environment restores the process's.
func TestContextWithDirAbsolute(t *testing.T) {
	rtx := NewContextFor(dashDef(), nil).WithDir("sub")
	wd, _ := os.Getwd()
	if got := rtx.Dir(); got != filepath.Join(wd, "sub") {
		t.Errorf("Dir = %q, want %q", got, filepath.Join(wd, "sub"))
	}
	t.Setenv("ROTINI_API_PROBE", "1")
	rtx.WithEnviron([]string{"X=1"}).WithEnviron(nil)
	if v, ok := rtx.LookupEnv("ROTINI_API_PROBE"); !ok || v != "1" {
		t.Errorf("WithEnviron(nil) did not restore the process environment: %q, %v", v, ok)
	}
}

// TestPartialInputsOutsideCompletion pins that PartialInputs reads the whole argv, leniently.
func TestPartialInputsOutsideCompletion(t *testing.T) {
	in, set, err := NewContextFor(ciDef(), []string{"--name", "bob", "--bogus", "--port", "x", "tgt"}).PartialInputs[ciInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Flags.Name != "bob" || in.App.Arguments.Target != "tgt" {
		t.Errorf("PartialInputs = %+v", in.App)
	}
	if _, ok := set["App.Flags.Port"]; ok {
		t.Error("a value that doesn't convert was marked set")
	}
	if _, ok := set["App.Flags.Mode"]; !ok {
		t.Error("the default was not in the Presence")
	}
}

// TestInputReportZero pins the zero InputReport: no fields, Validate nil, Format writes
// nothing.
func TestInputReportZero(t *testing.T) {
	var r InputReport
	if len(r.Fields()) != 0 {
		t.Error("Fields")
	}
	if _, ok := r.Winner("X"); ok || r.History("X") != nil {
		t.Error("Winner/History")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate = %v", err)
	}
	var buf bytes.Buffer
	if err := r.Format(&buf); err != nil || buf.Len() != 0 {
		t.Errorf("Format = %q, %v", buf.String(), err)
	}
}

// TestMergeInputsEdges pins MergeInputs: no layers is the zero value, an empty layer is the
// identity, a stale path copies nothing, and an unset field never clobbers a lower layer.
func TestMergeInputsEdges(t *testing.T) {
	if got := MergeInputs[ciInputs](); !reflect.DeepEqual(got, ciInputs{}) {
		t.Errorf("MergeInputs() = %+v", got)
	}
	low, set := ciValid()
	lowLayer := InputLayer[ciInputs]{Name: "low", Values: low, Set: set}
	got := MergeInputs(lowLayer, InputLayer[ciInputs]{}, InputLayer[ciInputs]{Set: Presence{"App.Flags.Nope": {}, "Nope": {}, "": {}}})
	if !reflect.DeepEqual(got, low) {
		t.Errorf("merge changed the value: %+v", got.App)
	}
	var high ciInputs
	high.App.Flags.Name = "carol"
	got, rep := MergeInputsWithReport(lowLayer, InputLayer[ciInputs]{Name: "high", Values: high, Set: Presence{"App.Flags.Name": {}}})
	if got.App.Flags.Name != "carol" || got.App.Arguments.Target != "x" {
		t.Errorf("merge = %+v", got.App)
	}
	if w, _ := rep.Winner("App.Flags.Name"); w.Layer != "high" || w.Raw != "" {
		t.Errorf("Winner = %+v (no rotini layer, so Raw stays as given)", w)
	}
	if h := rep.History("App.Flags.Name"); len(h) != 2 || h[0].Layer != "low" {
		t.Errorf("History = %+v", h)
	}
	// Merging into a non-struct T must not panic.
	_ = MergeInputs(InputLayer[int]{Values: 3, Set: Presence{"X": {}}})
}

// TestPresenceOfNonStruct pins PresenceOf on values that are not inputs structs: empty.
func TestPresenceOfNonStruct(t *testing.T) {
	if n := len(PresenceOf(3)); n != 0 {
		t.Errorf("PresenceOf(int) has %d entries", n)
	}
	in, _ := ciValid()
	if n := len(PresenceOf(&in)); n != 0 {
		t.Errorf("PresenceOf(pointer) has %d entries", n)
	}
	if n := len(PresenceOf(ciInputs{})); n != 0 {
		t.Errorf("PresenceOf(zero) has %d entries", n)
	}
}

// TestInputsWrongTypeIsError pins "A type that cannot be anchored there is an error, not a
// silent zero value" for every inputs method: a struct naming a flag the command lacks, a
// non-struct and a pointer are each an internal *ParseError, never a panic or a nil error.
func TestInputsWrongTypeIsError(t *testing.T) {
	type other struct {
		X struct {
			Flags struct {
				Q int `rotini:"q"`
			}
		}
	}
	rtx := NewContextFor(ciDef(), []string{"--name", "a", "t"})
	calls := map[string]func() error{
		"Inputs":           func() error { _, err := rtx.Inputs[other](); return err },
		"InputsWithReport": func() error { _, _, err := rtx.InputsWithReport[other](); return err },
		"ArgvInputs":       func() error { _, err := rtx.ArgvInputs[other](); return err },
		"EnvInputs":        func() error { _, err := rtx.EnvInputs[other](); return err },
		"FileInputs":       func() error { _, err := rtx.FileInputs[other](); return err },
		"StdinInputs":      func() error { _, err := rtx.StdinInputs[other](); return err },
		"DefaultInputs":    func() error { _, err := rtx.DefaultInputs[other](); return err },
		"PartialInputs":    func() error { _, _, err := rtx.PartialInputs[other](); return err },
		"CheckInputs":      func() error { return rtx.CheckInputs(other{}, nil) },

		"Inputs[int]":           func() error { _, err := rtx.Inputs[int](); return err },
		"Inputs[*T]":            func() error { _, err := rtx.Inputs[*ciInputs](); return err },
		"InputsWithReport[int]": func() error { _, _, err := rtx.InputsWithReport[int](); return err },
		"ArgvInputs[int]":       func() error { _, err := rtx.ArgvInputs[int](); return err },
		"EnvInputs[int]":        func() error { _, err := rtx.EnvInputs[int](); return err },
		"FileInputs[int]":       func() error { _, err := rtx.FileInputs[int](); return err },
		"StdinInputs[int]":      func() error { _, err := rtx.StdinInputs[int](); return err },
		"DefaultInputs[int]":    func() error { _, err := rtx.DefaultInputs[int](); return err },
		"PartialInputs[int]":    func() error { _, _, err := rtx.PartialInputs[int](); return err },
		"Read(*int)":            func() error { var n int; return NewInputReader(InputSettings{}).Read(rtx, &n) },
	}
	for name, call := range calls {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", name, r)
				}
			}()
			err = call()
		}()
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Kind != ParseKindInternal || CategoryOf(err) != CategoryInternal {
			t.Errorf("%s = %v, want an internal *ParseError", name, err)
		}
	}
}
