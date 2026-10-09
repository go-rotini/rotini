package rotini

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The conformance cases for how values are spelled: argument fallbacks and acquisition, custom
// negation, enum aliases and hidden and deprecated values, time layouts and relative times, the
// pattern kinds, and env list separators. Each binds a small CLI of its own.

// acShipDef is `ship <env> [service]`, both arguments falling back to the environment and the
// configuration file, the second to a default.
func acShipDef() Definition {
	return Definition{
		Name: "ship", Handler: "Ship",
		Arguments: []ArgDef{
			{Name: "env", Type: "string", Required: true},
			{Name: "service", Type: "string", Default: "api"},
		},
	}
}

type acShipInputs struct {
	Ship struct {
		Flags     struct{}
		Arguments struct {
			Env     string `rotini:"env" recon:"env" env:"SHIP_ENV"`
			Service string `rotini:"service" recon:"service" env:"SHIP_SERVICE"`
		}
	}
}

func readShip(t *testing.T, env []string, files []ConfigFile, argv ...string) (acShipInputs, error) {
	t.Helper()
	var in acShipInputs
	rtx := NewContextFor(acShipDef(), argv).WithEnviron(env)
	return in, NewInputReader(InputSettings{ConfigFiles: files}).Read(rtx, &in)
}

// ARG-17: an argument the command line leaves out reads its environment variable, which
// satisfies required; a typed argument wins.
func confArgumentEnvFallback(t *testing.T, _ *Context, _ InputSettings) {
	in, err := readShip(t, []string{"SHIP_ENV=prod"}, nil)
	if err != nil || in.Ship.Arguments.Env != "prod" || in.Ship.Arguments.Service != "api" {
		t.Errorf("env: %+v, %v", in.Ship.Arguments, err)
	}
	in, err = readShip(t, []string{"SHIP_ENV=prod"}, nil, "dev")
	if err != nil || in.Ship.Arguments.Env != "dev" {
		t.Errorf("argv over env: %+v, %v", in.Ship.Arguments, err)
	}
}

// ARG-18: a configuration file fills an argument after the environment, and before its default.
func confArgumentConfigFallback(t *testing.T, _ *Context, _ InputSettings) {
	path := filepath.Join(t.TempDir(), "ship.yaml")
	if err := os.WriteFile(path, []byte("env: staging\nservice: web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []ConfigFile{{Name: "ship", Path: path, Format: "yaml"}}
	in, err := readShip(t, []string{"SHIP_SERVICE=db"}, files)
	if err != nil || in.Ship.Arguments.Env != "staging" || in.Ship.Arguments.Service != "db" {
		t.Errorf("config and env: %+v, %v", in.Ship.Arguments, err)
	}
}

// ARG-19: an argument with `from` reads `-` from stdin and `@path` from a file.
func confArgumentFrom(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		Cat struct {
			Flags     struct{}
			Arguments struct {
				Input string `rotini:"input"`
			}
		}
	}
	def := Definition{Name: "cat", Handler: "Cat", Arguments: []ArgDef{{Name: "input", Type: "string", From: []string{"file", "stdin"}}}}
	var in inputs
	rtx := NewContextFor(def, []string{"-"}).WithStdin(strings.NewReader("piped\n"))
	if err := NewParser().Parse(rtx, &in); err != nil || in.Cat.Arguments.Input != "piped" {
		t.Errorf("-: %q, %v", in.Cat.Arguments.Input, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("filed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"@x"}).WithDir(dir), &in); err != nil || in.Cat.Arguments.Input != "filed" {
		t.Errorf("@x: %q, %v", in.Cat.Arguments.Input, err)
	}
}

// FLAG-19: `negatable: --plain` makes --plain the one negated form of --color.
func confCustomNegation(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags struct {
				Color bool `rotini:"color"`
			}
			Arguments struct{}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Default: "true", Negatable: true, Negation: "--plain"},
	}}
	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--plain"}), &in); err != nil || in.App.Flags.Color {
		t.Errorf("--plain: %v, %v", in.App.Flags.Color, err)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--no-color"}), &in); err == nil {
		t.Error("--no-color accepted beside a custom form")
	}
}

// FLAG-20: an alias binds as its value, a hidden value is accepted but not listed, and a
// deprecated value is reported.
func confEnumValueForms(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags struct {
				Format string `rotini:"format"`
			}
			Arguments struct{}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{
		Name: "format", Identifiers: []string{"--format"}, Type: "string", Enum: []string{"json", "yaml", "xml", "ini"},
		EnumValues: []EnumValue{{Value: "json"}, {Value: "yaml", Aliases: []string{"yml"}}, {Value: "xml", Hidden: true}, {Value: "ini", Deprecated: "use json"}},
	}}}
	parse := func(v string) (string, []Deprecation, error) {
		var in inputs
		rtx := NewContextFor(def, []string{"--format", v})
		err := NewParser().Parse(rtx, &in)
		return in.App.Flags.Format, Deprecations(rtx), err
	}
	if got, _, err := parse("yml"); err != nil || got != "yaml" {
		t.Errorf("yml: %q, %v", got, err)
	}
	if got, _, err := parse("xml"); err != nil || got != "xml" {
		t.Errorf("xml: %q, %v", got, err)
	}
	if _, deps, err := parse("ini"); err != nil || len(deps) != 1 || deps[0].Value != "ini" {
		t.Errorf("ini: %v, %v", deps, err)
	}
	if _, _, err := parse("toml"); err == nil || !strings.Contains(err.Error(), "(one of: json, yaml)") {
		t.Errorf("toml: %v", err)
	}
}

// FLAG-21: several layouts are tried in order, on argv and in the environment.
func confTimeLayouts(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags struct {
				At time.Time `rotini:"at"`
			}
			Arguments struct{}
			Env       struct {
				Since time.Time `rotini:"since" recon:"since" env:"SINCE" layout:"2006-01-02" layouts:"[\"2006-01-02\",\"unix\"]"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "at", Identifiers: []string{"--at"}, Type: "time.Time", Layout: "2006-01-02", Layouts: []string{"2006-01-02", "unix"}},
	}}
	var in inputs
	rtx := NewContextFor(def, []string{"--at", "1759104000"}).WithEnviron([]string{"SINCE=2026-09-29"})
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.App.Flags.At.Unix() != 1759104000 || !in.App.Env.Since.Equal(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("bound %+v", in.App)
	}
}

// FLAG-22: a relative time is measured from the run's clock, on argv, in the environment and
// in a default, all against one reading.
func confRelativeTime(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags struct {
				Since time.Time `rotini:"since"`
				Until time.Time `rotini:"until"`
			}
			Arguments struct{}
			Env       struct {
				After time.Time `rotini:"after" recon:"after" env:"AFTER" relative:"past"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "since", Identifiers: []string{"--since"}, Type: "time.Time", Relative: "past"},
		{Name: "until", Identifiers: []string{"--until"}, Type: "time.Time", Relative: "both", Default: "+1d"},
	}}
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var in inputs
	rtx := NewContextFor(def, []string{"--since", "2h"}).WithEnviron([]string{"AFTER=1w"}).WithClock(func() time.Time { return at })
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	f, e := in.App.Flags, in.App.Env
	if !f.Since.Equal(at.Add(-2*time.Hour)) || !f.Until.Equal(at.Add(24*time.Hour)) || !e.After.Equal(at.Add(-168*time.Hour)) || !rtx.Now().Equal(at) {
		t.Errorf("since=%v until=%v after=%v", f.Since, f.Until, e.After)
	}
}

// FLAG-23: regexp and glob values are checked when parsed.
func confPatternKinds(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags struct {
				Re   *regexp.Regexp `rotini:"re"`
				Glob Glob           `rotini:"glob"`
			}
			Arguments struct{}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "re", Identifiers: []string{"--re"}, Type: "*regexp.Regexp"},
		{Name: "glob", Identifiers: []string{"--glob"}, Type: "rotini.Glob"},
	}}
	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--re", "^a", "--glob", "*.go"}), &in); err != nil || !in.App.Flags.Re.MatchString("ab") || !in.App.Flags.Glob.Match("x.go") {
		t.Errorf("bound %+v, %v", in.App.Flags, err)
	}
	for _, argv := range [][]string{{"--re", "("}, {"--glob", "[a"}} {
		if err := NewParser().Parse(NewContextFor(def, argv), &in); err == nil {
			t.Errorf("%q accepted", argv)
		}
	}
}

// ENV-08: an env list declaring a separator splits on it, and its item count is checked after
// splitting.
func confEnvListSeparator(t *testing.T, _ *Context, _ InputSettings) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Path []string `rotini:"path" recon:"path,separator=:" env:"APP_PATH" minitems:"2"`
			}
		}
	}
	read := func(v string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron([]string{"APP_PATH=" + v})
		return in, NewInputReader(InputSettings{}).Read(rtx, &in)
	}
	if in, err := read("/a:/b"); err != nil || !slices.Equal(in.App.Env.Path, []string{"/a", "/b"}) {
		t.Errorf("/a:/b: %q, %v", in.App.Env.Path, err)
	}
	if _, err := read("/a"); err == nil {
		t.Error("one item passed minItems: 2")
	}
}
