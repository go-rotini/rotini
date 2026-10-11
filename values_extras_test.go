package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// A custom negated identifier replaces the derived one: it sets the flag false, takes no value,
// and --no-<x> is no longer accepted.
func TestParse_customNegation(t *testing.T) {
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
	parse := func(argv ...string) (inputs, error) {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, argv), &in)
		return in, err
	}
	if in, err := parse("--plain"); err != nil || in.App.Flags.Color {
		t.Errorf("--plain: color=%v err=%v", in.App.Flags.Color, err)
	}
	if in, err := parse("--plain", "--color"); err != nil || !in.App.Flags.Color {
		t.Errorf("--plain --color: color=%v err=%v", in.App.Flags.Color, err)
	}
	if _, err := parse("--plain=true"); err == nil || !strings.Contains(err.Error(), "negated form and takes no value") {
		t.Errorf("--plain=true: err = %v", err)
	}
	_, err := parse("--no-color")
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Kind != ParseKindUnknownFlag || !slices.Contains(pe.Candidates, "--plain") || slices.Contains(pe.Candidates, "--no-color") {
		t.Errorf("--no-color: err = %v", err)
	}
}

// Aliases bind as their value on the command line (as typed, in an = form, in a list), hidden
// values are accepted but not offered, and deprecated values are left out of candidates.
func TestParse_enumAliasesHiddenDeprecated(t *testing.T) {
	values := []EnumValue{
		{Value: "json"},
		{Value: "yaml", Aliases: []string{"yml"}, DeprecatedAliases: []string{"yml"}},
		{Value: "xml", Hidden: true},
		{Value: "ini", Deprecated: "going away", Aliases: []string{"cfg"}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Format string   `rotini:"format"`
				Tags   []string `rotini:"tag"`
			}
			Arguments struct {
				Kind string `rotini:"kind"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "format", Identifiers: []string{"--format"}, Type: "string", Enum: []string{"json", "yaml", "xml", "ini"}, EnumValues: values},
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string", Separator: ",", Enum: []string{"json", "yaml", "xml", "ini"}, EnumValues: values, IgnoreCase: true},
		},
		Arguments: []ArgDef{{Name: "kind", Type: "string", Enum: []string{"json", "yaml", "xml", "ini"}, EnumValues: values}},
	}
	parse := func(argv ...string) (inputs, *Context, error) {
		var in inputs
		rtx := NewContextFor(def, argv)
		return in, rtx, NewParser().Parse(rtx, &in)
	}
	in, rtx, err := parse("--format=yml", "--tag", "YML,xml,Cfg", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Flags.Format != "yaml" || !slices.Equal(in.App.Flags.Tags, []string{"yaml", "xml", "ini"}) || in.App.Arguments.Kind != "ini" {
		t.Errorf("bound %+v", in.App)
	}
	var got []string
	for _, d := range Deprecations(rtx) {
		got = append(got, d.Error())
	}
	want := []string{
		`flag "--format" value "yml" is deprecated`,
		`flag "--tag" value "YML" is deprecated`,
		`flag "--tag" value "Cfg" is deprecated: going away`,
		`argument "<kind>" value "cfg" is deprecated: going away`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("deprecations:\n got %q\nwant %q", got, want)
	}
	_, _, err = parse("--format", "toml", "json")
	var pe *ParseError
	if !errors.As(err, &pe) || !slices.Equal(pe.Candidates, []string{"json", "yaml"}) || !strings.Contains(pe.Msg, "(one of: json, yaml)") {
		t.Errorf("err = %v (%+v)", err, pe)
	}
	if cands := enumCandidates(flagEnum(def.Flags[0])); !slices.Equal(cands, []string{"json", "yaml"}) {
		t.Errorf("completion offers %q", cands)
	}
}

// Env and config inputs accept an enum's aliases, through the enumalias tag, and their messages
// leave out the values the enumunlisted tag names.
func TestInputReader_channelEnumAliases(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Format string `rotini:"format" recon:"format" env:"FORMAT" enum:"[\"json\",\"yaml\",\"xml\"]" enumalias:"{\"yml\":\"yaml\"}" enumunlisted:"[\"xml\"]"`
			}
		}
	}
	read := func(env string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron([]string{"FORMAT=" + env})
		return in, NewInputReader(InputSettings{}).Read(rtx, &in)
	}
	if in, err := read("yml"); err != nil || in.App.Env.Format != "yaml" {
		t.Errorf("yml: %q, %v", in.App.Env.Format, err)
	}
	if in, err := read("xml"); err != nil || in.App.Env.Format != "xml" {
		t.Errorf("xml: %q, %v", in.App.Env.Format, err)
	}
	if _, err := read("toml"); err == nil || !strings.Contains(err.Error(), "(one of: json, yaml)") {
		t.Errorf("toml: %v", err)
	}
}

// regexp and glob values are compiled or checked when parsed, from argv and env alike.
func TestParse_patternKinds(t *testing.T) {
	type inputs struct {
		App struct {
			Flags struct {
				Re      *regexp.Regexp `rotini:"re"`
				Include []Glob         `rotini:"include"`
			}
			Arguments struct{}
			Env       struct {
				Skip Glob `rotini:"skip" recon:"skip" env:"SKIP"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "re", Identifiers: []string{"--re"}, Type: "*regexp.Regexp"},
		{Name: "include", Identifiers: []string{"--include"}, Type: "[]rotini.Glob"},
	}}
	read := func(env string, argv ...string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(def, argv).WithEnviron([]string{"SKIP=" + env})
		return in, NewInputReader(InputSettings{}).Read(rtx, &in)
	}
	in, err := read("*.tmp", "--re", "^v[0-9]+$", "--include", "*.go", "--include", "cmd/*")
	if err != nil {
		t.Fatal(err)
	}
	f := in.App.Flags
	if !f.Re.MatchString("v12") || len(f.Include) != 2 || !f.Include[1].Match(filepath.Join("cmd", "x")) || !in.App.Env.Skip.Match("a.tmp") {
		t.Errorf("bound %+v", in.App)
	}
	if _, err := read("", "--re", "("); err == nil || !strings.Contains(err.Error(), `--re: "(" is not a valid regular expression (missing closing ): `+"`(`)") {
		t.Errorf("--re (: %v", err)
	}
	if _, err := read("", "--include", "[a"); err == nil || !strings.Contains(err.Error(), `"[a" is not a valid glob pattern`) {
		t.Errorf("--include [a: %v", err)
	}
	if _, err := read("[a"); err == nil {
		t.Error("SKIP=[a accepted")
	}
}

// Glob matches slash-separated names, a Windows name included, and ** is not recursive.
func TestGlob_Match(t *testing.T) {
	g := Glob("src/*.go")
	for name, want := range map[string]bool{"src/a.go": true, `src\a.go`: os.PathSeparator == '\\', "src/x/a.go": false, "a.go": false} {
		if got := g.Match(name); got != want {
			t.Errorf("Match(%q) = %v, want %v", name, got, want)
		}
	}
	if Glob("**/a").Match("x/y/a") {
		t.Error("** matched across segments")
	}
	var bad Glob
	if err := bad.UnmarshalText([]byte(`a\`)); err == nil {
		t.Error("a trailing escape was accepted")
	}
	if text, _ := Glob("*.md").MarshalText(); string(text) != "*.md" {
		t.Errorf("MarshalText = %q", text)
	}
}

// An argument's env and config fallback fills a position the command line left out:
// argv > env > config > default, a gap stops the fill, a variadic splits on its separator, a
// required argument is satisfied, and an error names where the value came from.
func TestInputReader_argumentFallback(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				Env     string    `rotini:"env" recon:"env" env:"DEPLOY_ENV"`
				Service string    `rotini:"service" recon:"service" env:"DEPLOY_SERVICE"`
				Since   time.Time `rotini:"since" recon:"since" env:"DEPLOY_SINCE"`
				Hosts   []string  `rotini:"hosts" recon:"hosts" env:"DEPLOY_HOSTS"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "env", Type: "string", Required: true, Enum: []string{"dev", "prod"}},
		{Name: "service", Type: "string", Default: "api"},
		{Name: "since", Type: "time.Time", Relative: "past", Default: "24h"},
		{Name: "hosts", Type: "[]string", Variadic: true, Separator: ","},
	}}
	cfg := writeConfig(t, "service: web\n")
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	read := func(env []string, config bool, argv ...string) (inputs, error) {
		var in inputs
		meta := InputSettings{}
		if config {
			meta.ConfigFiles = []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}
		}
		rtx := NewContextFor(def, argv).WithEnviron(env).WithClock(func() time.Time { return at })
		return in, NewInputReader(meta).Read(rtx, &in)
	}

	in, err := read([]string{"DEPLOY_ENV=prod", "DEPLOY_HOSTS=a, b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	a := in.App.Arguments
	if a.Env != "prod" || a.Service != "web" || !a.Since.Equal(at.Add(-24*time.Hour)) || !slices.Equal(a.Hosts, []string{"a", "b"}) {
		t.Errorf("env and config: %+v", a)
	}

	in, err = read([]string{"DEPLOY_ENV=prod", "DEPLOY_SERVICE=svc"}, true, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if a := in.App.Arguments; a.Env != "dev" || a.Service != "svc" {
		t.Errorf("argv then env: %+v", a)
	}

	if _, err := read(nil, false); err == nil || !strings.Contains(err.Error(), "missing required input: <env>") {
		t.Errorf("no env: %v", err)
	}
	if _, err := read([]string{"DEPLOY_ENV=qa"}, false); err == nil || !strings.Contains(err.Error(), `invalid value "qa" for <env> (one of: dev, prod) (from environment variable DEPLOY_ENV)`) {
		t.Errorf("bad env: %v", err)
	}
	_, err = read([]string{"DEPLOY_ENV=dev", "DEPLOY_SINCE=tomorrow"}, false)
	var ie *InputError
	if !errors.As(err, &ie) || ie.Channel != "argument" || !strings.Contains(err.Error(), "<since>: ") || !strings.Contains(err.Error(), "(from environment variable DEPLOY_SINCE)") {
		t.Errorf("bad since: %v", err)
	}
}

// The per-channel layers place an argument's fallback too, so a merged report names the layer
// that supplied it.
func TestInputLayers_argumentFallback(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				Env string `rotini:"env" recon:"env" env:"DEPLOY_ENV"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "env", Type: "string", Required: true}}}
	rtx := NewContextFor(def, nil).WithEnviron([]string{"DEPLOY_ENV=prod"})
	envLayer, err := rtx.EnvInputs[inputs]()
	if err != nil {
		t.Fatal(err)
	}
	argv, err := rtx.ArgvInputs[inputs]()
	if err != nil {
		t.Fatal(err)
	}
	in, report := MergeInputsWithReport(envLayer, argv)
	if in.App.Arguments.Env != "prod" {
		t.Errorf("merged %+v", in.App)
	}
	if src, ok := report.Winner(FieldPath("App.Arguments.Env")); !ok || src.Layer != "env" {
		t.Errorf("winner = %+v, %v", src, ok)
	}
	if err := report.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// An argument before any variadic one takes `from`: @file reads a file, - reads stdin.
func TestParse_argumentFrom(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				Input string   `rotini:"input"`
				Rest  []string `rotini:"rest"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "input", Type: "string", From: []string{"file", "stdin"}},
		{Name: "rest", Type: "[]string", Variadic: true, From: []string{"file"}},
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := func(stdin string, argv ...string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(def, argv).WithDir(dir).WithStdin(strings.NewReader(stdin))
		return in, NewParser().Parse(rtx, &in)
	}
	if in, err := parse("", "@in.txt", "@x"); err != nil || in.App.Arguments.Input != "from file" || !slices.Equal(in.App.Arguments.Rest, []string{"@x"}) {
		t.Errorf("@file: %+v, %v", in.App.Arguments, err)
	}
	if in, err := parse("piped\n", "-"); err != nil || in.App.Arguments.Input != "piped" {
		t.Errorf("-: %+v, %v", in.App.Arguments, err)
	}
	if in, err := parse("", "@@me"); err != nil || in.App.Arguments.Input != "@me" {
		t.Errorf("@@: %+v, %v", in.App.Arguments, err)
	}
	if _, err := parse("", "@missing"); err == nil || !strings.Contains(err.Error(), `<input>: no such file: "missing"`) {
		t.Errorf("missing: %v", err)
	}
}

// An env list input splits on its separator, and item counts and per-item rules are checked
// against the split items, not the unsplit text.
func TestInputReader_envListSeparator(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Paths []string `rotini:"paths" recon:"paths,separator=:" env:"PATHS" minitems:"2" maxitems:"3" pattern:"^/"`
			}
		}
	}
	read := func(v string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron([]string{"PATHS=" + v})
		return in, NewInputReader(InputSettings{}).Read(rtx, &in)
	}
	if in, err := read("/a:/b,c"); err != nil || !slices.Equal(in.App.Env.Paths, []string{"/a", "/b,c"}) {
		t.Errorf("split: %q, %v", in.App.Env.Paths, err)
	}
	if _, err := read("/a"); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Errorf("one item: %v", err)
	}
	if _, err := read("/a:/b:/c:/d"); err == nil || !strings.Contains(err.Error(), "at most 3") {
		t.Errorf("four items: %v", err)
	}
	if _, err := read("/a:b"); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Errorf("pattern: %v", err)
	}
}
