package rotini

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A flag's env or config fallback supplies a value the user never typed, so an error about
// that value names where it came from, as a value of the wrong type already did.

type fsInputs struct {
	App struct {
		Flags struct {
			Port int    `rotini:"port" recon:"port" env:"PORT"`
			Mode string `rotini:"mode" recon:"mode" env:"MODE"`
		}
		Arguments struct{}
	}
}

func fsDef(secret bool) Definition {
	high := 100.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Secret: secret, Maximum: &high},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"fast", "slow"}},
		},
	}
}

func fsRead(t *testing.T, def Definition, argv []string, meta InputSettings) error {
	t.Helper()
	var in fsInputs
	return NewInputReader(meta).Read(NewContextFor(def, argv), &in)
}

func TestFallbackSource_namedInValueErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		argv []string
		want string // "" = no error
	}{
		{"env bound", map[string]string{"PORT": "500"}, nil,
			"app: --port must be <= 100 (got 500) (from environment variable PORT)"},
		{"env enum", map[string]string{"MODE": "fastest"}, nil,
			`app: invalid value "fastest" for --mode (one of: fast, slow) (from environment variable MODE)`},
		{"argv bound names no source", nil, []string{"--port", "500"},
			"app: --port must be <= 100 (got 500)"},
		{"argv overrides an env value", map[string]string{"PORT": "500"}, []string{"--port", "5"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			err := fsRead(t, fsDef(false), tt.argv, InputSettings{})
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Read = %v, want no error", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Read = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFallbackSource_argvValueHasNoSuffixEvenWithEnvSet(t *testing.T) {
	t.Setenv("PORT", "5")
	err := fsRead(t, fsDef(false), []string{"--port", "500"}, InputSettings{})
	if err == nil || strings.Contains(err.Error(), "(from") {
		t.Fatalf("Read = %v, want a bound error without a source: the value was typed", err)
	}
}

func TestFallbackSource_configFile(t *testing.T) {
	cfg := writeConfig(t, "port: 500\n")
	err := fsRead(t, fsDef(false), nil, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})
	if err == nil || !strings.HasSuffix(err.Error(), "(from configuration file "+cfg+", key port)") {
		t.Fatalf("Read = %v, want the config file named", err)
	}
}

func TestFallbackSource_secretValueRedactedNameShown(t *testing.T) {
	t.Setenv("PORT", "500")
	err := fsRead(t, fsDef(true), nil, InputSettings{})
	if err == nil {
		t.Fatal("Read = nil, want a bound error")
	}
	msg := err.Error()
	if strings.Contains(msg, "500") || !strings.Contains(msg, "environment variable PORT") {
		t.Errorf("message %q: want the value redacted and the variable named", msg)
	}
}

// TestFallbackSource_reportMatchesInputs pins parity: the overlay path's report names the same
// source with the same words as rtx.Inputs.
func TestFallbackSource_reportMatchesInputs(t *testing.T) {
	t.Setenv("PORT", "500")
	rtx := NewContextFor(fsDef(false), nil)
	_, inputsErr := rtx.Inputs[fsInputs]()
	_, _, reportErr := rtx.InputsWithReport[fsInputs]()
	if inputsErr == nil || reportErr == nil || inputsErr.Error() != reportErr.Error() {
		t.Fatalf("Inputs = %v, InputsWithReport = %v; want the same error", inputsErr, reportErr)
	}
}

// fsRuleInputs carries one flag per value rule, each with an environment fallback.
type fsRuleInputs struct {
	App struct {
		Flags struct {
			Step  int      `rotini:"step" recon:"step" env:"STEP"`
			Name  string   `rotini:"name" recon:"name" env:"NAME"`
			Code  string   `rotini:"code" recon:"code" env:"CODE"`
			Sku   string   `rotini:"sku" recon:"sku" env:"SKU"`
			Tags  []string `rotini:"tags" recon:"tags" env:"TAGS"`
			Token string   `rotini:"token" recon:"token" env:"TOKEN"`
		}
		Arguments struct{}
	}
}

func fsRuleDef() Definition {
	five := 5.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "step", Identifiers: []string{"--step"}, Type: "int", MultipleOf: &five},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", MinLength: 3},
			{Name: "code", Identifiers: []string{"--code"}, Type: "string", Pattern: "^[a-z]+$"},
			{Name: "sku", Identifiers: []string{"--sku"}, Type: "string", Pattern: "^[A-Z]{3}$", PatternMessage: "three capital letters"},
			{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Separator: ",", MaxItems: new(1)},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
		},
	}
}

// TestFallbackSource_everyValueRule pins the suffix on each kind of value rule, and its
// absence on a missing required input, which no source supplied.
func TestFallbackSource_everyValueRule(t *testing.T) {
	tests := []struct {
		env, value string
		want       string // a substring of the message
	}{
		{"STEP", "7", "(from environment variable STEP)"},
		{"NAME", "ab", "(from environment variable NAME)"},
		{"CODE", "ABC", "(from environment variable CODE)"},
		{"SKU", "abc", `three capital letters (got "abc") (from environment variable SKU)`},
		{"TAGS", "a,b", "(from environment variable TAGS)"},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("TOKEN", "t")
			t.Setenv(tt.env, tt.value)
			var in fsRuleInputs
			err := NewInputReader(InputSettings{}).Read(NewContextFor(fsRuleDef(), nil), &in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Read = %v, want a message containing %q", err, tt.want)
			}
		})
	}
	t.Run("missing required carries no source", func(t *testing.T) {
		var in fsRuleInputs
		err := NewInputReader(InputSettings{}).Read(NewContextFor(fsRuleDef(), nil), &in)
		if err == nil || !strings.Contains(err.Error(), "--token") || strings.Contains(err.Error(), "(from") {
			t.Fatalf("Read = %v, want a missing --token without a source", err)
		}
	})
}

type fsListInputs struct {
	App struct {
		Flags struct {
			Tags []string `rotini:"tags" recon:"tags"`
		}
		Arguments struct{}
		Config    struct {
			Nums []int `rotini:"nums" recon:"nums"`
		}
	}
}

type fsTimeFlagInputs struct {
	App struct {
		Flags struct {
			At time.Time `rotini:"at" recon:"at"`
		}
		Arguments struct{}
	}
}

type fsTimeConfigInputs struct {
	App struct {
		Arguments struct{}
		Config    struct {
			At time.Time `rotini:"at" recon:"at"`
		}
	}
}

func fsListDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string"},
		{Name: "at", Identifiers: []string{"--at"}, Type: "time"},
	}}
}

func fsConfigRead(t *testing.T, cfg string, in any) error {
	t.Helper()
	return NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).Read(NewContextFor(fsListDef(), nil), in)
}

// A null item in a configuration list is refused, by a flag's fallback and by a config input,
// with a message that says so; it is never read as empty text.
func TestFallbackSource_nullListItem(t *testing.T) {
	for body, want := range map[string]string{
		"tags: [a, null]\n": "app: --tags: list item 2 is null (from configuration file %s, key tags)",
		"nums: [1, ~]\n":    "app: config key nums: list item 2 is null (from configuration file %s)",
	} {
		cfg := writeConfig(t, body)
		want = fmt.Sprintf(want, cfg)
		set := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
		for reader, err := range readerErrors[fsListInputs](NewContextFor(fsListDef(), nil).WithInputSettings(set), "files") {
			if err == nil || err.Error() != want || !errors.Is(err, ErrUsage) {
				t.Errorf("%q: %s: %v, want %q", body, reader, err, want)
			}
		}
	}
}

// A time flag's fallback reads a configuration value as a config input of type time reads the
// same key: both take it, as the same instant, or both refuse it.
func TestFallbackSource_timeAgreesWithConfigInput(t *testing.T) {
	for _, v := range []string{"2024-01-02T03:04:05Z", `"2024-01-02T03:04:05"`, `"2024-01-02 03:04"`, `"2024-01-02"`, `"03:04:05"`, `"junk"`} {
		cfg := writeConfig(t, "at: "+v+"\n")
		var fl fsTimeFlagInputs
		var ci fsTimeConfigInputs
		ferr, cerr := fsConfigRead(t, cfg, &fl), fsConfigRead(t, cfg, &ci)
		if (ferr == nil) != (cerr == nil) || !fl.App.Flags.At.Equal(ci.App.Config.At) {
			t.Errorf("at: %s: flag %v (%v), config input %v (%v)", v, fl.App.Flags.At, ferr, ci.App.Config.At, cerr)
		}
	}
}

type fsArgInputs struct {
	App struct {
		Flags struct {
			Explain bool `rotini:"explain"`
		}
		Arguments struct {
			Dir string `rotini:"dir" recon:"dir" env:"DIR" expand:"env"`
			N   int    `rotini:"n" recon:"n" env:"N"`
		}
	}
}

// An argument's fallback that can't be read or expanded is a usage error naming where it came
// from. A short-circuit flag skips a configuration file's, not the environment's.
func TestFallbackSource_argumentValueErrors(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:     []FlagDef{{Name: "explain", Identifiers: []string{"--explain"}, Type: "bool", ShortCircuit: true}},
		Arguments: []ArgDef{{Name: "dir", Type: "string", Default: "."}, {Name: "n", Type: "int", Default: "1"}},
	}
	tests := []struct {
		name, config string
		env          []string
		want         string // a substring of the error; "" = none
		waived       bool   // whether --explain skips it
	}{
		{"list", "n: [1, null]\n", nil, "<n> takes one value, not a list (from configuration file ", true},
		{"map", "dir: {a: b}\n", nil, "<dir> takes one value, not a map (from configuration file ", true},
		{"config expansion", "dir: $NOPE/x\n", nil, `<dir>: $NOPE is not set (in "$NOPE/x") (from configuration file `, true},
		{"env expansion", "", []string{"DIR=$NOPE"}, `<dir>: $NOPE is not set (in "$NOPE") (from environment variable DIR)`, false},
		{"env type", "", []string{"N=x"}, `<n>: "x" is not a valid integer (from environment variable N)`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, layer := InputSettings{}, "env"
			if tt.config != "" {
				set.ConfigFiles = []ConfigFile{{Name: "app", Path: writeConfig(t, tt.config), Format: "yaml"}}
				layer = "files"
			}
			for _, argv := range [][]string{nil, {"--explain"}} {
				want := tt.want
				if len(argv) > 0 && tt.waived {
					want = ""
				}
				for reader, err := range readerErrors[fsArgInputs](NewContextFor(def, argv).WithEnviron(tt.env).WithInputSettings(set), layer) {
					if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want) || !errors.Is(err, ErrUsage)) {
						t.Errorf("%v: %s = %v, want %q", argv, reader, err, want)
					}
				}
			}
		})
	}
}

type fsShadowInputs struct {
	App struct {
		Flags struct {
			Port int `rotini:"port" recon:"port" env:"PORT"`
		}
		Arguments struct {
			N int `rotini:"n" recon:"n" env:"N"`
		}
	}
}

// A fallback that a higher source overrides is never read, so a value there that its type
// can't hold is no error, for Inputs and InputsWithReport alike; one that wins still is.
func TestFallbackSource_overriddenBadValueIsNotRead(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:     []FlagDef{{Name: "port", Identifiers: []string{"--port"}, Type: "int"}},
		Arguments: []ArgDef{{Name: "n", Type: "int"}},
	}
	tests := []struct {
		name     string
		config   string
		env      []string
		argv     []string
		port, n  int
		wantFrom string // "" = no error
	}{
		{"argv over env", "", []string{"PORT=abc", "N=abc"}, []string{"--port", "1", "2"}, 1, 2, ""},
		{"argv over config", "port: abc\nn: abc\n", nil, []string{"--port", "1", "2"}, 1, 2, ""},
		{"env over config", "port: abc\nn: abc\n", []string{"PORT=3", "N=4"}, nil, 3, 4, ""},
		{"config wins", "port: abc\n", nil, nil, 0, 0, "configuration file"},
		{"env wins over config", "port: 5\n", []string{"PORT=abc"}, nil, 0, 0, "environment variable PORT"},
		{"argument env wins", "", []string{"N=abc"}, []string{"--port", "1"}, 0, 0, "environment variable N"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := InputSettings{}
			if tt.config != "" {
				set.ConfigFiles = []ConfigFile{{Name: "app", Path: writeConfig(t, tt.config), Format: "yaml"}}
			}
			rtx := NewContextFor(def, tt.argv).WithEnviron(tt.env).WithInputSettings(set)
			in, err := rtx.Inputs[fsShadowInputs]()
			merged, _, rerr := rtx.InputsWithReport[fsShadowInputs]()
			for name, got := range map[string]struct {
				in  fsShadowInputs
				err error
			}{"Inputs": {in, err}, "InputsWithReport": {merged, rerr}} {
				if tt.wantFrom != "" {
					if got.err == nil || !strings.Contains(got.err.Error(), "(from "+tt.wantFrom) {
						t.Errorf("%s = %v, want an error from %s", name, got.err, tt.wantFrom)
					}
					continue
				}
				if got.err != nil || got.in.App.Flags.Port != tt.port || got.in.App.Arguments.N != tt.n {
					t.Errorf("%s: port %d, n %d, err %v; want %d, %d", name, got.in.App.Flags.Port, got.in.App.Arguments.N, got.err, tt.port, tt.n)
				}
			}
		})
	}
}
