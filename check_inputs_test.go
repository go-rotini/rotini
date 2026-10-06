package rotini

import (
	"errors"
	"strings"
	"testing"
)

type ciInputs struct {
	App struct {
		Flags struct {
			Help  bool     `rotini:"help"`
			Name  string   `rotini:"name"`
			Level string   `rotini:"level"`
			Port  int      `rotini:"port"`
			Tags  []string `rotini:"tags"`
			Mode  string   `rotini:"mode"`
			A     bool     `rotini:"a"`
			B     bool     `rotini:"b"`
			Ratio *float64 `rotini:"ratio"`
		}
		Arguments struct {
			Target string `rotini:"target"`
		}
		Env struct {
			Region string `rotini:"region" recon:"region,required" env:"REGION" enum:"[\"us\",\"eu\"]"`
		}
	}
}

func ciDef() Definition {
	high := 10.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", Required: true},
			{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}},
			{Name: "port", Identifiers: []string{"-p", "--port"}, Type: "int", Constraints: Constraints{Maximum: &high}},
			{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Constraints: Constraints{MinItems: 2}},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Required: true, Default: "fast"},
			{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
			{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64"},
		},
		FlagGroups: []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"a", "b"}}},
		Arguments:  []ArgDef{{Name: "target", Type: "string", Required: true}},
	}
}

// ciValid is a fully valid value with its Presence.
func ciValid() (ciInputs, Presence) {
	var in ciInputs
	in.App.Flags.Name = "alice"
	in.App.Flags.Tags = []string{"a", "b"}
	in.App.Arguments.Target = "x"
	in.App.Env.Region = "us"
	return in, PresenceOf(in)
}

func TestCheckInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ciInputs, Presence)
		kind   ParseKind // ParseKindUnspecified: no *ParseError expected
		want   string    // substring of the error; "" means no error
	}{
		{"valid", func(*ciInputs, Presence) {}, 0, ""},
		{"required flag absent from the set", func(_ *ciInputs, s Presence) { delete(s, "App.Flags.Name") },
			ParseKindMissingRequired, "missing required input: --name"},
		{"required argument absent", func(_ *ciInputs, s Presence) { delete(s, "App.Arguments.Target") },
			ParseKindMissingRequired, "missing required input: <target>"},
		{"enum", func(in *ciInputs, s Presence) { in.App.Flags.Level = "mid"; s["App.Flags.Level"] = InputSource{} },
			ParseKindEnumViolation, `invalid value "mid" for --level (one of: low, high)`},
		{"bound", func(in *ciInputs, s Presence) { in.App.Flags.Port = 99; s["App.Flags.Port"] = InputSource{} },
			ParseKindConstraintViolation, "--port must be <= 10 (got 99)"},
		{"item count", func(in *ciInputs, s Presence) {
			in.App.Flags.Tags = []string{"one"}
			s["App.Flags.Tags"] = InputSource{}
		},
			ParseKindConstraintViolation, "--tags needs at least 2 values (got 1)"},
		{"absent list counts as zero items", func(in *ciInputs, s Presence) { in.App.Flags.Tags = nil; delete(s, "App.Flags.Tags") },
			ParseKindConstraintViolation, "--tags needs at least 2 values (got 0)"},
		{"unsupplied zero value is not checked", func(in *ciInputs, _ Presence) { in.App.Flags.Level = "" }, 0, ""},
		{"supplied value not in the set is not checked", func(in *ciInputs, s Presence) { in.App.Flags.Level = "mid"; delete(s, "App.Flags.Level") }, 0, ""},
		{"flag group", func(in *ciInputs, s Presence) {
			in.App.Flags.A, in.App.Flags.B = true, true
			s["App.Flags.A"], s["App.Flags.B"] = InputSource{}, InputSource{}
		}, ParseKindConstraintViolation, "--a"},
		{"short-circuit waives everything", func(in *ciInputs, s Presence) {
			in.App.Flags.Help = true
			s["App.Flags.Help"] = InputSource{}
			delete(s, "App.Flags.Name")
			in.App.Flags.Port = 99
			s["App.Flags.Port"] = InputSource{}
		}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, set := ciValid()
			tt.mutate(&in, set)
			err := NewContextFor(ciDef(), nil).CheckInputs(in, set)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckInputs = %v, want nil", err)
				}
				return
			}
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Kind != tt.kind || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CheckInputs = %v, want ParseKind %v containing %q", err, tt.kind, tt.want)
			}
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
			}
		})
	}
}

func TestCheckInputs_channels(t *testing.T) {
	t.Run("required env absent", func(t *testing.T) {
		in, set := ciValid()
		delete(set, "App.Env.Region")
		err := NewContextFor(ciDef(), nil).CheckInputs(in, set)
		var ie *InputError
		if !errors.As(err, &ie) || !strings.Contains(err.Error(), "is required") || CategoryOf(err) != CategoryUsage {
			t.Fatalf("CheckInputs = %v, want a usage *InputError for the required env input", err)
		}
	})
	t.Run("env enum", func(t *testing.T) {
		in, set := ciValid()
		in.App.Env.Region = "mars"
		err := NewContextFor(ciDef(), nil).CheckInputs(in, set)
		if err == nil || !strings.Contains(err.Error(), "REGION") || !strings.Contains(err.Error(), "mars") {
			t.Fatalf("CheckInputs = %v, want the env enum violation naming REGION", err)
		}
	})
}

func TestCheckInputs_wrongTypeIsInternal(t *testing.T) {
	var other struct {
		App struct {
			Flags struct {
				Nope string `rotini:"nope"`
			}
		}
	}
	err := NewContextFor(ciDef(), nil).CheckInputs(other, Presence{})
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Kind != ParseKindInternal {
		t.Fatalf("CheckInputs(foreign type) = %v, want ParseKindInternal", err)
	}
}

func TestCheckInputs_neverChangesValue(t *testing.T) {
	in, set := ciValid()
	in.App.Flags.Level = "HIGH"
	set["App.Flags.Level"] = InputSource{}
	_ = NewContextFor(ciDef(), nil).CheckInputs(in, set)
	if in.App.Flags.Level != "HIGH" {
		t.Errorf("Level = %q, want the value left as given", in.App.Flags.Level)
	}
}

func TestPresenceOf(t *testing.T) {
	var in ciInputs
	in.App.Flags.Name = "alice"
	zero := 0.0
	in.App.Flags.Ratio = &zero
	set := PresenceOf(in)
	for _, want := range []FieldPath{"App.Flags.Name", "App.Flags.Ratio"} {
		if _, ok := set[want]; !ok {
			t.Errorf("PresenceOf missing %s", want)
		}
	}
	for _, absent := range []FieldPath{"App.Flags.Port", "App.Flags.Tags", "App.Arguments.Target", "App.Env.Region"} {
		if _, ok := set[absent]; ok {
			t.Errorf("PresenceOf marked zero field %s", absent)
		}
	}
	if set["App.Flags.Name"].Layer != "custom" {
		t.Errorf("layer = %q, want custom", set["App.Flags.Name"].Layer)
	}
}

// TestInputReport_validatesHandBuiltLayers covers the fixed merge: a hand-built layer's values
// are checked, its fields count as supplied, and a hand-built-only merge points at CheckInputs.
func TestInputReport_validatesHandBuiltLayers(t *testing.T) {
	t.Setenv("REGION", "us")
	rtx := NewContextFor(ciDef(), []string{"x"})
	argv, err := rtx.ArgvInputs[ciInputs]()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := rtx.DefaultInputs[ciInputs]()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a hand-built value is checked", func(t *testing.T) {
		var hand ciInputs
		hand.App.Flags.Name, hand.App.Flags.Level = "alice", "mid"
		hand.App.Flags.Tags = []string{"a", "b"}
		layer := InputLayer[ciInputs]{Name: "vault", Values: hand, Set: PresenceOf(hand)}
		_, report := MergeInputsWithReport(defaults, argv, layer)
		if err := report.Validate(); err == nil || !strings.Contains(err.Error(), `invalid value "mid" for --level`) {
			t.Fatalf("Validate = %v, want the hand-built enum violation", err)
		}
	})
	t.Run("a hand-built value satisfies a required input", func(t *testing.T) {
		var hand ciInputs
		hand.App.Flags.Name = "alice"
		hand.App.Flags.Tags = []string{"a", "b"}
		layer := InputLayer[ciInputs]{Name: "vault", Values: hand, Set: PresenceOf(hand)}
		_, report := MergeInputsWithReport(defaults, argv, layer)
		if err := report.Validate(); err != nil {
			t.Fatalf("Validate = %v, want nil: the hand-built layer supplies --name", err)
		}
	})
	t.Run("without it the required input is still missing", func(t *testing.T) {
		_, report := MergeInputsWithReport(defaults, argv)
		if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "--name") {
			t.Fatalf("Validate = %v, want --name missing", err)
		}
	})
	t.Run("hand-built only has no command context", func(t *testing.T) {
		var hand ciInputs
		hand.App.Flags.Name = "alice"
		_, report := MergeInputsWithReport(InputLayer[ciInputs]{Name: "vault", Values: hand, Set: PresenceOf(hand)})
		var pe *ParseError
		if err := report.Validate(); !errors.As(err, &pe) || pe.Kind != ParseKindInternal || !strings.Contains(err.Error(), "CheckInputs") {
			t.Fatalf("Validate = %v, want ParseKindInternal pointing at CheckInputs", err)
		}
	})
}

// TestAbsentListItemCount pins one answer for a list nobody supplied: the command line,
// CheckInputs and InputReport.Validate all count it as zero items against minItems, and a
// declared default supplies the items instead.
func TestAbsentListItemCount(t *testing.T) {
	type in struct {
		App struct {
			Flags struct {
				Tags []string `rotini:"tags"`
				Keys []string `rotini:"keys"`
			}
			Arguments struct {
				Files []string `rotini:"files"`
			}
		}
	}
	def := func(files int) Definition {
		return Definition{
			Name: "app", Handler: "App",
			Flags: []FlagDef{
				{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Constraints: Constraints{MinItems: 1}},
				{Name: "keys", Identifiers: []string{"--keys"}, Type: "[]string", Defaults: []string{"a", "b"}, Constraints: Constraints{MinItems: 2}},
			},
			Arguments: []ArgDef{{Name: "files", Type: "[]string", Variadic: true, Constraints: Constraints{MinItems: files}}},
		}
	}

	if err := NewParser().Parse(NewContextFor(def(0), nil), &struct{}{}); err == nil || !strings.Contains(err.Error(), "--tags needs at least 1 value (got 0)") {
		t.Fatalf("command line: %v, want --tags at least 1", err)
	}

	var v in
	if err := NewContextFor(def(0), nil).CheckInputs(v, Presence{}); err == nil || !strings.Contains(err.Error(), "--tags needs at least 1 value (got 0)") {
		t.Fatalf("CheckInputs: %v, want --tags at least 1", err)
	}

	v.App.Flags.Tags = []string{"x"}
	if err := NewContextFor(def(2), nil).CheckInputs(v, PresenceOf(v)); err == nil || !strings.Contains(err.Error(), "<files> needs at least 2 values (got 0)") {
		t.Fatalf("CheckInputs: %v, want <files> at least 2", err)
	}
	if err := NewContextFor(def(0), nil).CheckInputs(v, PresenceOf(v)); err != nil {
		t.Fatalf("CheckInputs: %v, want nil: --keys has a default", err)
	}

	rtx := NewContextFor(def(0), nil)
	argv, err := rtx.ArgvInputs[in]()
	if err != nil {
		t.Fatal(err)
	}
	var hand in
	hand.App.Flags.Keys = []string{"k", "l"}
	_, report := MergeInputsWithReport(argv, InputLayer[in]{Name: "vault", Values: hand, Set: PresenceOf(hand)})
	if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "--tags needs at least 1 value (got 0)") {
		t.Fatalf("Validate: %v, want --tags at least 1", err)
	}
}
