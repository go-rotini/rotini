package rotini

import (
	"errors"
	"fmt"
	"testing"
)

// wordingDef has a sub-command, so a message about it names the whole command path.
func wordingDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "name", Identifiers: []string{"-n", "--name"}, Type: "string"},
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "count"},
			{Name: "all", Identifiers: []string{"-a", "--all"}, Type: "bool"},
			{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Negatable: true},
			{Name: "label", Identifiers: []string{"--label"}, Type: "map[string]string"},
		},
		Commands: []CommandDef{
			{Name: "greet", Handler: "Greet", Arguments: []ArgDef{{Name: "who", Type: "string"}}},
			{Name: "ping", Handler: "Ping"},
		},
	}
}

type wordingCfgInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Port int    `rotini:"port" recon:"api.port" max:"100"`
			Tier string `rotini:"tier" recon:"tier" enum:"[\"gold\",\"silver\"]"`
		}
	}
}

// TestUsageErrorWording pins the text of each usage error: names the program declares appear
// bare (flags as the user typed them, variables, config keys, command paths), tokens the user
// typed appear quoted.
func TestUsageErrorWording(t *testing.T) {
	parse := []struct {
		name string
		argv []string
		want string
	}{
		{"a long flag needs a value", []string{"--name"}, "--name needs a value"},
		{"a short flag needs a value", []string{"-n"}, "-n needs a value"},
		{"a clustered flag needs a value", []string{"-an"}, "-n needs a value"},
		{"a count flag takes no value", []string{"--verbose=3"}, "--verbose counts occurrences and takes no value"},
		{"digits after a count flag", []string{"-v3"}, `-v counts occurrences and takes no value; repeat it instead (-vvv), not "-v3"`},
		{"digits after a clustered count flag", []string{"-av2"}, `-v counts occurrences and takes no value; repeat it instead (-vv), not "-v2"`},
		{"many digits after a count flag", []string{"-vvv10"}, `-v counts occurrences and takes no value; repeat it instead (-vvv), not "-v10"`},
		{"digits after a bool flag", []string{"-a3"}, `-a takes no value (got "-a3")`},
		{"digits then letters keep the unknown-flag error", []string{"-v3x"}, `unknown flag "-3"`},
		{"a negated form takes no value", []string{"--no-color=x"}, "--no-color is the negated form and takes no value; use --color to set one"},
		{"a map value needs key=value", []string{"--label", "v"}, `--label expects key=value pairs (got "v")`},
		{"a map key must not be empty", []string{"--label", "=v"}, `--label needs a key before "=" (got "=v")`},
		{"a map key must not be whitespace", []string{"--label", " =v"}, `--label needs a key before "=" (got " =v")`},
		{"too many arguments name the command path", []string{"greet", "ann", "bob"}, "app greet accepts at most 1 argument (got 2)"},
		{"no arguments name the command path", []string{"ping", "x"}, "app ping takes no arguments (got 1)"},
		{"an unknown flag is quoted", []string{"--nope"}, `unknown flag "--nope"`},
	}
	for _, tc := range parse {
		t.Run(tc.name, func(t *testing.T) {
			err := NewParser().Parse(NewContextFor(wordingDef(), tc.argv), &struct{}{})
			if err == nil || err.Error() != tc.want {
				t.Errorf("Parse(%q) = %v, want %q", tc.argv, err, tc.want)
			}
			if err != nil && CategoryOf(err) != CategoryUsage {
				t.Errorf("Parse(%q) = %v, want a usage error", tc.argv, err)
			}
		})
	}

	env := []struct {
		name string
		env  []string
		want string
	}{
		{"a required env input names every variable", nil, "environment variable APP_REGION (or REGION) is required"},
		{"env coercion names the variable set", []string{"REGION=us", "APP_PORT=abc"}, "environment variable APP_PORT: expected int"},
		{"an env map key must not be whitespace", []string{"APP_REGION=us", "APP_LABELS= =v"}, `environment variable APP_LABELS needs a key before "=" (got "=v")`},
	}
	for _, tc := range env {
		t.Run(tc.name, func(t *testing.T) {
			if err := stEnvRead(t, tc.env...); err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}

	config := []struct {
		name, file, want string
	}{
		{"config coercion names the key and file", "api:\n  port: abc\n", "config key api.port: expected int (from configuration file %s)"},
		{"a config constraint names the key and file", "api:\n  port: 500\n", "config key api.port must be <= 100 (got 500) (from configuration file %s)"},
		{"a config enum names the key and file", "tier: bronze\n", `invalid value "bronze" for config key tier (one of: gold, silver) (from configuration file %s)`},
	}
	for _, tc := range config {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.file)
			var in wordingCfgInputs
			err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path}}}).Read(stRTX(nil), &in)
			if want := fmt.Sprintf(tc.want, path); err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
			if err != nil && !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v, want a usage error", err)
			}
		})
	}
}

// TestDigitsAfterSwitch_declaredDigit pins that digits after a count flag are read as flags when
// a digit flag is declared: -v3 is -v and -3.
func TestDigitsAfterSwitch_declaredDigit(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "verbose", Identifiers: []string{"-v"}, Type: "count"},
		{Name: "three", Identifiers: []string{"-3"}, Type: "bool"},
	}}
	var in struct {
		App struct {
			Flags struct {
				Verbose int  `rotini:"verbose"`
				Three   bool `rotini:"three"`
			}
			Arguments struct{}
		}
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"-v3"}), &in); err != nil || in.App.Flags.Verbose != 1 || !in.App.Flags.Three {
		t.Errorf("-v3 = %+v, %v; want -v once and -3 set", in.App.Flags, err)
	}
}

// TestDigitsAfterSwitch_noCandidates pins that the error for -v3 offers a Suggestor nothing.
func TestDigitsAfterSwitch_noCandidates(t *testing.T) {
	err := NewParser().Parse(NewContextFor(wordingDef(), []string{"-v3"}), &struct{}{})
	pe, ok := errors.AsType[*ParseError](err)
	if !ok || pe.Kind != ParseKindInvalidValue || pe.Flag != "-v" || pe.Token != "-v3" || pe.Candidates != nil {
		t.Errorf("err = %#v, want an invalid value for -v with no candidates", err)
	}
}
