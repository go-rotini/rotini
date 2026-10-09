package rotini

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestIsFlag_negativeNumbers pins the one rule every argv walker shares: only "-" followed by
// a digit, or by "." and a digit, is a number; any other dash word is a flag.
func TestIsFlag_negativeNumbers(t *testing.T) {
	for tok, want := range map[string]bool{
		"-5": false, "-0.5": false, "-.5": false, "-1e3": false, "-5s": false, "-46": false,
		"-Inf": true, "-inf": true, "-INF": true, "-Infinity": true, "-nan": true, "-NaN": true,
		"-0x1p3": false, "-.": true, "-.x": true, "-v": true, "--5": true, "--verbose": true,
		"-": false, "--": false, "": false, "5": false, "x": false,
	} {
		if got := isFlag(nil, tok); got != want {
			t.Errorf("isFlag(%q) = %v, want %v", tok, got, want)
		}
	}
}

func infDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags:     []FlagDef{{Name: "include", Identifiers: []string{"-I", "--include"}, Type: "string"}},
		Arguments: []ArgDef{{Name: "nums", Type: "[]float64", Variadic: true}},
	}
}

type infInputs struct {
	App struct {
		Flags struct {
			Include string `rotini:"include"`
		}
		Arguments struct {
			Nums []float64 `rotini:"nums"`
		}
	}
}

// TestParse_declaredShortBeatsFloatWord pins that a word spelling a float name ("-Inf") is the
// declared short flag with an attached value, while real negative numbers stay positionals.
func TestParse_declaredShortBeatsFloatWord(t *testing.T) {
	for _, c := range []struct {
		argv    []string
		include string
		nums    []float64
	}{
		{[]string{"-Inf"}, "nf", nil},
		{[]string{"-INF"}, "NF", nil},
		{[]string{"-Infinity"}, "nfinity", nil},
		{[]string{"-.5", "-5", "-1e3", "-Inf"}, "nf", []float64{-0.5, -5, -1000}},
	} {
		var in infInputs
		if err := NewParser().Parse(NewContextFor(infDef(), c.argv), &in); err != nil {
			t.Fatalf("Parse(%v): %v", c.argv, err)
		}
		if in.App.Flags.Include != c.include || !reflect.DeepEqual(in.App.Arguments.Nums, c.nums) {
			t.Errorf("Parse(%v) = include %q, nums %v; want %q, %v", c.argv, in.App.Flags.Include, in.App.Arguments.Nums, c.include, c.nums)
		}
	}
}

// TestParse_floatNameIsAFlagWord pins that "-inf" is no longer a number: with no -i declared it
// is an unknown flag, and a negative non-finite value is passed after "--".
func TestParse_floatNameIsAFlagWord(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "n", Type: "float64"}}}
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				N float64 `rotini:"n"`
			}
		}
	}
	var in inputs
	err := NewParser().Parse(NewContextFor(def, []string{"-inf"}), &in)
	if pe, ok := errors.AsType[*ParseError](err); !ok || pe.Kind != ParseKindUnknownFlag {
		t.Fatalf("-inf: err = %v, want an unknown-flag error", err)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--", "-inf"}), &in); err != nil || in.App.Arguments.N > -1e308 {
		t.Errorf("-- -inf: n = %v, err = %v; want -Inf", in.App.Arguments.N, err)
	}
}

// TestWalkers_agreeOnFloatWords pins that the resolver and completion walk read "-Inf" as the
// flag (skipping its separate value when it takes one) and "-5" as the first positional.
func TestWalkers_agreeOnFloatWords(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "inf", Identifiers: []string{"-Inf"}, Type: "string"}},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub",
			Arguments: []ArgDef{{Name: "n", Type: "[]string", Variadic: true}}}},
	}
	chain, _ := resolveChain(def, []string{"-Inf", "x", "sub"})
	if got := chainNames(chain); !slices.Equal(got, []string{"app", "sub"}) {
		t.Errorf("resolve -Inf x sub = %v, want [app sub]", got)
	}
	chain, _ = resolveChain(def, []string{"-5", "sub"})
	if got := chainNames(chain); !slices.Equal(got, []string{"app"}) {
		t.Errorf("resolve -5 sub = %v, want [app] (a number is the first positional)", got)
	}
	if cc := walkContext(def, []string{"-Inf", "x", "sub"}); len(cc.chain) != 2 || cc.positionals != 0 {
		t.Errorf("walk -Inf x sub: chain %d, positionals %d; want 2, 0", len(cc.chain), cc.positionals)
	}
	if cc := walkContext(def, []string{"sub", "-Inf"}); cc.pending == nil || cc.pending.fd.Name != "inf" {
		t.Errorf("walk sub -Inf: pending %+v, want -Inf awaiting a value", cc.pending)
	}
}

func pluginHostDef() Definition {
	return Definition{
		Name: "demo", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help", "-h"}, Type: "bool", ShortCircuit: true},
			{Name: "verbose", Identifiers: []string{"--verbose", "-v"}, Type: "bool"},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string"},
		},
		Plugins: []PluginDef{{Name: "sig", Binary: "demo-sig"}},
		Commands: []CommandDef{{Name: "grp", Handler: "AppRun",
			PluginDiscovery: &PluginDiscoveryDef{Prefix: "demo-grp-"}}},
	}
}

// TestDefaultResolver_flagsBeforePlugin pins the strict reading of the words before a plugin's
// name: a short-circuit flag cancels the dispatch, any other flag is a usage error, and a bare
// plugin name still dispatches.
func TestDefaultResolver_flagsBeforePlugin(t *testing.T) {
	def := pluginHostDef()

	res, err := DefaultResolver(def, []string{"sig", "--verbose"})
	if err != nil || res.Plugin == nil || !slices.Equal(res.Plugin.Args, []string{"--verbose"}) {
		t.Fatalf("sig --verbose: plugin %+v, err %v; want a dispatch passing --verbose on", res.Plugin, err)
	}

	for _, argv := range [][]string{{"--help", "sig", "x"}, {"-h", "sig"}, {"grp", "--help", "found"}} {
		res, err := DefaultResolver(def, argv)
		if err != nil || res.Plugin != nil {
			t.Errorf("%v: plugin %+v, err %v; want no dispatch", argv, res.Plugin, err)
			continue
		}
		cut := argv[:slices.Index(argv, "--help")+1]
		if argv[0] == "-h" {
			cut = argv[:1]
		}
		if !slices.Equal(res.Argv, cut) {
			t.Errorf("%v: argv = %v, want %v", argv, res.Argv, cut)
		}
	}

	for _, c := range []struct {
		argv []string
		kind ParseKind
		flag string
	}{
		{[]string{"-v", "sig"}, ParseKindMisplacedFlag, "-v"},
		{[]string{"--name", "x", "sig"}, ParseKindMisplacedFlag, "--name"},
		{[]string{"--name=x", "sig"}, ParseKindMisplacedFlag, "--name"},
		{[]string{"--help=false", "sig"}, ParseKindMisplacedFlag, "--help"},
		{[]string{"grp", "-v", "found"}, ParseKindMisplacedFlag, "-v"},
		{[]string{"--bogus", "sig"}, ParseKindUnknownFlag, "--bogus"},
	} {
		_, err := DefaultResolver(def, c.argv)
		pe, ok := errors.AsType[*ParseError](err)
		if !ok || pe.Kind != c.kind || pe.Flag != c.flag || CategoryOf(err) != CategoryUsage {
			t.Errorf("%v: err = %#v, want a %v usage error for %s", c.argv, err, c.kind, c.flag)
		}
	}
	_, err = DefaultResolver(def, []string{"-v", "sig"})
	if want := `-v can't come before plugin "sig": a plugin receives only the words after its name`; err == nil || err.Error() != want {
		t.Errorf("message = %v, want %q", err, want)
	}
}

// TestRun_flagBeforePluginIsUsageError pins that the run reports the misplaced flag as an
// error (not a fault), exits 1 and never starts the plugin; --help before the name runs the host.
func TestRun_flagBeforePluginIsUsageError(t *testing.T) {
	writeFakeBinary(t, "demo-sig", "#!/bin/sh\necho \"sig ran: $*\"\n")
	p, out, errb := pluginProgram(pluginHostDef(), []string{"-v", "sig", "x"})
	code, err := p.Run(p.args)
	if code != 1 || CategoryOf(err) != CategoryUsage || strings.Contains(out.String(), "sig ran") {
		t.Fatalf("run = %d, %v (stdout %q); want exit 1 with a usage error and no plugin run", code, err, out)
	}
	if got := errb.String(); !strings.Contains(got, `can't come before plugin "sig"`) || strings.Contains(got, "panic") {
		t.Errorf("stderr = %q, want the misplaced-flag error line", got)
	}

	p, out, _ = pluginProgram(pluginHostDef(), []string{"--help", "sig"})
	if code, err := p.Run(p.args); code != 0 || err != nil || strings.Contains(out.String(), "sig ran") {
		t.Errorf("--help sig: run = %d, %v (stdout %q); want the host to answer", code, err, out)
	}
}

// TestParse_subCommandAfterDoubleDash pins the message for a real sub-command typed after
// "--": it names "--" as the reason, and the word is not among the candidates.
func TestParse_subCommandAfterDoubleDash(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{Name: "run", Handler: "AppRun", Aliases: []string{"r"}},
			{Name: "stop", Handler: "AppStop"},
		},
	}
	var in struct{}
	err := NewParser().Parse(NewContextFor(def, []string{"--", "run"}), &in)
	pe, ok := errors.AsType[*ParseError](err)
	if !ok || pe.Kind != ParseKindUnknownCommand {
		t.Fatalf("-- run: err = %v, want an unknown-command error", err)
	}
	if want := `"run" after "--" is not read as a command, and "app" takes no arguments`; pe.Msg != want {
		t.Errorf("message = %q, want %q", pe.Msg, want)
	}
	if want := []string{"r", "stop"}; !slices.Equal(pe.Candidates, want) {
		t.Errorf("candidates = %v, want %v", pe.Candidates, want)
	}

	// A mistyped word keeps the usual message, "--" or not.
	err = NewParser().Parse(NewContextFor(def, []string{"--", "rnu"}), &in)
	if err == nil || err.Error() != `unknown command "rnu" for "app"` {
		t.Errorf("-- rnu: err = %v, want the unknown-command message", err)
	}
}
