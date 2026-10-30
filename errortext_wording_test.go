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
			{Name: "ping", Handler: "Ping", Flags: []FlagDef{
				{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int"},
				{Name: "user", Identifiers: []string{"-u", "--user"}, Type: "string"},
				{Name: "token", Identifiers: []string{"-t", "--token"}, Type: "string", Secret: true},
			}},
		},
	}
}

// wordingPingInputs binds ping's flags, so a value that doesn't read is an error.
type wordingPingInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
	}
	AppPing struct {
		Flags struct {
			Count int    `rotini:"count"`
			User  string `rotini:"user"`
			Token string `rotini:"token"`
		}
		Arguments struct{}
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

// TestUsageErrorWording pins the text of each usage error: a parse error starts with the path of
// the command that rejected it; names the program declares appear bare (flags as the user typed
// them, variables, config keys, command paths), tokens the user typed appear quoted.
func TestUsageErrorWording(t *testing.T) {
	parse := []struct {
		name string
		argv []string
		want string
	}{
		{"a long flag needs a value", []string{"--name"}, "app: --name needs a value"},
		{"a short flag needs a value", []string{"-n"}, "app: -n needs a value"},
		{"a clustered flag needs a value", []string{"-an"}, "app: -n needs a value"},
		{"a count flag takes no value", []string{"--verbose=3"}, "app: --verbose counts occurrences and takes no value"},
		{"digits after a count flag", []string{"-v3"}, `app: -v counts occurrences and takes no value; repeat it instead (-vvv), not "-v3"`},
		{"digits after a clustered count flag", []string{"-av2"}, `app: -v counts occurrences and takes no value; repeat it instead (-vv), not "-v2"`},
		{"many digits after a count flag", []string{"-vvv10"}, `app: -v counts occurrences and takes no value; repeat it instead (-vvv), not "-v10"`},
		{"digits after a bool flag", []string{"-a3"}, `app: -a takes no value (got "-a3")`},
		{"digits then letters keep the unknown-flag error", []string{"-v3x"}, `app: unknown flag "-3"`},
		{"a negated form takes no value", []string{"--no-color=x"}, "app: --no-color is the negated form and takes no value; use --color to set one"},
		{"a map value needs key=value", []string{"--label", "v"}, `app: --label expects key=value pairs (got "v")`},
		{"a map key must not be empty", []string{"--label", "=v"}, `app: --label needs a key before "=" (got "=v")`},
		{"a map key must not be whitespace", []string{"--label", " =v"}, `app: --label needs a key before "=" (got " =v")`},
		{"too many arguments", []string{"greet", "ann", "bob"}, "app greet: accepts at most 1 argument (got 2)"},
		{"no arguments quote the word", []string{"ping", "x"}, `app ping: takes no arguments, got "x"`},
		{"no arguments count the other words", []string{"ping", "x", "y", "z"}, `app ping: takes no arguments, got "x" and 2 more`},
		{"an unknown flag is quoted", []string{"--nope"}, `app: unknown flag "--nope"`},
		{"a flag error names the command that rejected it", []string{"ping", "--nope"}, `app ping: unknown flag "--nope"`},
		{"a flag before a sub-command is the ancestor's", []string{"--nope", "ping"}, `app: unknown flag "--nope"`},
		{"an unknown command names its parent", []string{"pnig"}, `app: unknown command "pnig"`},
		{"a long flag with one dash says how it was read", []string{"ping", "-user", "x"}, `app ping: takes no arguments, got "x"; -user was read as -u "ser"`},
		{"a secret value stays out of the hint", []string{"ping", "-token", "x"}, `app ping: takes no arguments, got "x"; -token was read as -t [redacted]`},
		{"no hint when the word is not the flag's long name", []string{"ping", "-uname", "x"}, `app ping: takes no arguments, got "x"`},
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

	t.Run("a value error says how a one-dash long flag was read", func(t *testing.T) {
		var in wordingPingInputs
		err := NewParser().Parse(NewContextFor(wordingDef(), []string{"ping", "-count"}), &in)
		if want := `app ping: -c: "ount" is not a valid integer; -count was read as -c "ount"`; err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})

	env := []struct {
		name string
		env  []string
		want string
	}{
		{"a required env input names every variable", nil, "app: environment variable APP_REGION (or REGION) is required"},
		{"env coercion names the variable set", []string{"REGION=us", "APP_PORT=abc"}, `app: environment variable APP_PORT: "abc" is not a valid integer`},
		{"an env map key must not be whitespace", []string{"APP_REGION=us", "APP_LABELS= =v"}, `app: environment variable APP_LABELS needs a key before "=" (got "=v")`},
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
		{"config coercion names the key and file", "api:\n  port: abc\n", `app: config key api.port: "abc" is not a valid integer (from configuration file %s)`},
		{"a config constraint names the key and file", "api:\n  port: 500\n", "app: config key api.port must be <= 100 (got 500) (from configuration file %s)"},
		{"a config enum names the key and file", "tier: bronze\n", `app: invalid value "bronze" for config key tier (one of: gold, silver) (from configuration file %s)`},
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
