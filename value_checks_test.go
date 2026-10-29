package rotini

import (
	"errors"
	"math"
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
			err := checkConstraints(allFeatures(), "--ratio", "float64", "", c, []string{v}, false, "")
			if err == nil || !strings.Contains(err.Error(), "--ratio must be a finite number (got "+v+")") {
				t.Errorf("%s with %s: err = %v, want a finite-number error", v, kind, err)
			}
		}
		if err := checkConstraints(allFeatures(), "--ratio", "float64", "", Constraints{}, []string{v}, false, ""); err != nil {
			t.Errorf("%s unbounded: err = %v, want accepted", v, err)
		}
	}
	if err := checkNumberBounds("ratio", Constraints{ExclusiveMinimum: new(0.0)}, math.NaN(), "NaN", formatNum); err == nil {
		t.Error("a typed NaN passed exclusiveMinimum: 0")
	}
}

// TestConstraints_documentedSemantics pins what each Constraints field's comment promises:
// lengths count runes, a pattern need only occur in the value, a zero maximum or minimum
// pointer is an enforced bound, and a zero MinLength or MinItems is unset.
func TestConstraints_documentedSemantics(t *testing.T) {
	cases := []struct {
		name   string
		typ    string
		c      Constraints
		values []string
		ok     bool
	}{
		{"min length in runes", "string", Constraints{MinLength: 2}, []string{"éé"}, true},
		{"max length in runes", "string", Constraints{MaxLength: new(1)}, []string{"é"}, true},
		{"max length exceeded", "string", Constraints{MaxLength: new(1)}, []string{"ab"}, false},
		{"zero max length enforced", "string", Constraints{MaxLength: new(0)}, []string{"a"}, false},
		{"zero min length unset", "string", Constraints{MinLength: 0}, []string{""}, true},
		{"pattern is contained", "string", Constraints{Pattern: "b"}, []string{"abc"}, true},
		{"pattern anchors when written so", "string", Constraints{Pattern: "^b"}, []string{"abc"}, false},
		{"minimum: 0 enforced", "int", Constraints{Minimum: new(0.0)}, []string{"-1"}, false},
		{"minimum inclusive", "int", Constraints{Minimum: new(0.0)}, []string{"0"}, true},
		{"exclusive minimum strict", "int", Constraints{ExclusiveMinimum: new(0.0)}, []string{"0"}, false},
		{"exclusive maximum strict", "int", Constraints{ExclusiveMaximum: new(3.0)}, []string{"3"}, false},
		{"maximum inclusive", "int", Constraints{Maximum: new(3.0)}, []string{"3"}, true},
		{"multiple of a fraction", "float64", Constraints{MultipleOf: new(0.1)}, []string{"0.3"}, true},
		{"not a multiple", "int", Constraints{MultipleOf: new(4.0)}, []string{"6"}, false},
		{"maxItems: 0 enforced", "[]string", Constraints{MaxItems: new(0)}, []string{"a"}, false},
		{"minItems: 0 unset", "[]string", Constraints{MinItems: 0}, nil, true},
		{"minItems counts items", "[]string", Constraints{MinItems: 2}, []string{"a"}, false},
	}
	for _, c := range cases {
		err := checkConstraints(allFeatures(), "--x", c.typ, "", c.c, c.values, false, "")
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil {
			if pe, ok := errors.AsType[*ParseError](err); !ok || pe.Kind != ParseKindConstraintViolation {
				t.Errorf("%s: err = %#v, want a constraint-violation *ParseError", c.name, err)
			}
		}
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

// TestURLValue_hostPortWithoutScheme pins that "localhost:8080", with or without a path, is not
// taken as a URL with the scheme "localhost", while real opaque URLs still parse.
func TestURLValue_hostPortWithoutScheme(t *testing.T) {
	parse := parseURLValue
	for _, s := range []string{"localhost:8080", "example.com:443", "localhost:8080/api", "db:5432/", "localhost:8080?x=1"} {
		_, err := parse(s)
		if err == nil || !strings.Contains(err.Error(), "as in http://"+s) {
			t.Errorf("%q: err = %v, want the scheme hint", s, err)
		}
	}
	for _, s := range []string{"mailto:ada@example.com", "urn:isbn:0451450523", "tel:+1-201-555-0123", "tel:5551234", "http://localhost:8080"} {
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
