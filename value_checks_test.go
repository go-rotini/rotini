package rotini

import (
	"errors"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// TestCheckNumberBounds_nonFinite pins that any declared bound requires a finite number, from
// every bound kind, and that an unbounded float still accepts NaN and ±Inf.
func TestCheckNumberBounds_nonFinite(t *testing.T) {
	bounds := map[string]Constraints{
		"minimum":          {Minimum: new(0.0)},
		"maximum":          {Maximum: new(1.0)},
		"exclusiveMinimum": {ExclusiveMinimum: new(0.0)},
		"exclusiveMaximum": {ExclusiveMaximum: new(1.0)},
		"multipleOf":       {MultipleOf: new(0.5)},
	}
	for _, v := range []string{"nan", "NaN", "inf", "+Inf", "-inf", "infinity"} {
		for kind, c := range bounds {
			err := checkConstraints("--ratio", "float64", c, []string{v}, false, "")
			if err == nil || !strings.Contains(err.Error(), "--ratio must be a finite number (got "+v+")") {
				t.Errorf("%s with %s: err = %v, want a finite-number error", v, kind, err)
			}
		}
		if err := checkConstraints("--ratio", "float64", Constraints{}, []string{v}, false, ""); err != nil {
			t.Errorf("%s unbounded: err = %v, want accepted", v, err)
		}
	}
	if err := checkNumberBounds("ratio", Constraints{ExclusiveMinimum: new(0.0)}, math.NaN(), "NaN", formatNum); err == nil {
		t.Error("a typed NaN passed exclusiveMinimum: 0")
	}
}

// TestParse_nonFiniteAgainstBounds pins the rule end to end on the command line, the
// environment and a typed value checked by CheckInputs.
func TestParse_nonFiniteAgainstBounds(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64",
			ExclusiveMinimum: new(0.0)}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Ratio float64 `rotini:"ratio"`
			}
			Arguments struct{}
		}
	}
	for _, v := range []string{"nan", "inf"} {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, []string{"--ratio", v}), &in)
		if pe, ok := errors.AsType[*ParseError](err); !ok || pe.Kind != ParseKindConstraintViolation {
			t.Errorf("--ratio %s: err = %v, want a constraint violation", v, err)
		}
	}
	var in inputs
	in.App.Flags.Ratio = math.NaN()
	rtx := NewContextFor(def, nil)
	if err := rtx.CheckInputs(in, PresenceOf(in)); err == nil || !strings.Contains(err.Error(), "must be a finite number") {
		t.Errorf("CheckInputs(NaN) = %v, want a finite-number error", err)
	}
}

// TestURLValue_hostPortWithoutScheme pins that "localhost:8080" is not taken as a URL with the
// scheme "localhost", while real opaque URLs still parse.
func TestURLValue_hostPortWithoutScheme(t *testing.T) {
	parse := valueParsers[reflect.TypeFor[*url.URL]()]
	for _, s := range []string{"localhost:8080", "example.com:443"} {
		_, err := parse(s)
		if err == nil || !strings.Contains(err.Error(), "as in http://"+s) {
			t.Errorf("%q: err = %v, want the scheme hint", s, err)
		}
	}
	for _, s := range []string{"mailto:ada@example.com", "urn:isbn:0451450523", "http://localhost:8080"} {
		if _, err := parse(s); err != nil {
			t.Errorf("%q: err = %v, want accepted", s, err)
		}
	}
}

// TestParse_mapEmptyKey pins that a map pair needs a key before its "=", on the command line
// and in a short-circuited run.
func TestParse_mapEmptyKey(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "label", Identifiers: []string{"--label"}, Type: "map[string]string"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
		},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Label map[string]string `rotini:"label"`
				Help  bool              `rotini:"help"`
			}
			Arguments struct{}
		}
	}
	for _, argv := range [][]string{{"--label", "=v"}, {"--label=", "--label", "a=1"}, {"--help", "--label", "=v"}} {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, argv), &in)
		pe, ok := errors.AsType[*ParseError](err)
		if argv[0] == "--label=" {
			if !ok || !strings.Contains(pe.Msg, "expects key=value pairs") {
				t.Errorf("%v: err = %v, want a key=value error", argv, err)
			}
			continue
		}
		if !ok || pe.Kind != ParseKindInvalidValue || pe.Msg != `--label needs a key before "=" (got "=v")` {
			t.Errorf("%v: err = %v, want the empty-key error", argv, err)
		}
	}
	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--label", "a="}), &in); err != nil || in.App.Flags.Label["a"] != "" {
		t.Errorf("a=: err = %v, want an empty value accepted", err)
	}
}

// TestInputs_nonFiniteFromEnvironment pins that a flag's env fallback meets the same rule, and
// that the error names the variable.
func TestInputs_nonFiniteFromEnvironment(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64",
			ExclusiveMinimum: new(0.0)}},
	}
	var in struct {
		App struct {
			Flags struct {
				Ratio float64 `rotini:"ratio" recon:"ratio" env:"RATIO"`
			}
			Arguments struct{}
		}
	}
	t.Setenv("RATIO", "nan")
	err := NewInputReader(InputSettings{}).Read(NewContextFor(def, nil), &in)
	if want := "--ratio must be a finite number (got nan) (from environment variable RATIO)"; err == nil || err.Error() != want {
		t.Errorf("Read = %v, want %q", err, want)
	}
}
