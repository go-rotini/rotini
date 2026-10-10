package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func hiddenDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "remove", Aliases: []string{"rm"}, HiddenAliases: []string{"del", "erase"}, Handler: "AppRemove",
			DeprecatedIdentifiers: []string{"erase"},
			Flags:                 []FlagDef{{Name: "output", Identifiers: []string{"--output", "-o"}, HiddenIdentifiers: []string{"--out"}, Type: "string"}},
		}},
	}
}

func TestHiddenAliases_dispatchButAreNotListed(t *testing.T) {
	rtx := NewContextFor(hiddenDef(), []string{"del", "x"})
	if got := rtx.Command(); got.Name != "remove" || got.Matched != "del" {
		t.Errorf("resolved %q (matched %q), want remove via del", got.Name, got.Matched)
	}
	if got := complete(hiddenDef(), []string{""}, nil, nil); slices.Contains(got, "del") || slices.Contains(got, "erase") || !slices.Contains(got, "rm") {
		t.Errorf("command candidates = %v, want no hidden alias", got)
	}
	if got := complete(hiddenDef(), []string{"remove", "-"}, nil, nil); slices.Contains(got, "--out") || !slices.Contains(got, "--output") {
		t.Errorf("flag candidates = %v, want no hidden identifier", got)
	}
	// A flag set through a hidden identifier counts as set, so it isn't offered again, and a
	// hidden value-taking identifier consumes its value word.
	if got := complete(hiddenDef(), []string{"remove", "--out", "x", "-"}, nil, nil); slices.Contains(got, "--output") {
		t.Errorf("after --out x, candidates = %v, want --output already set", got)
	}
	if got := complete(hiddenDef(), []string{"del", "--out", ""}, nil, nil); len(got) != 0 {
		t.Errorf("--out's value completes %v, want nothing", got)
	}
	cands := childCommandNames(rtx.CommandChain()[0])
	if slices.Contains(cands, "del") {
		t.Errorf("suggestion candidates %v include a hidden alias", cands)
	}
}

func TestHiddenAliases_deprecated(t *testing.T) {
	got := Deprecations(NewContextFor(hiddenDef(), []string{"erase"}))
	want := []Deprecation{{Kind: "command", Name: "remove", Identifier: "erase"}}
	if !slices.Equal(got, want) {
		t.Errorf("Deprecations = %+v, want %+v", got, want)
	}
	if got := Deprecations(NewContextFor(hiddenDef(), []string{"del"})); len(got) != 0 {
		t.Errorf("a hidden alias not listed as deprecated reports %+v", got)
	}
}

func TestHiddenAliases_shadowDiscoveredPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"app-del", "app-sync"} {
		if err := os.WriteFile(filepath.Join(dir, progFile(n)), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := hiddenDef()
	def.PluginDiscovery, def.PluginPath = &PluginDiscoveryDef{Prefix: "app-"}, dir
	var names []string
	for _, p := range NewContextFor(def, nil).Command().DiscoveredPlugins() {
		names = append(names, p.Name)
	}
	if slices.Contains(names, "del") || !slices.Contains(names, "sync") {
		t.Errorf("discovered %v, want sync and not del, which the hidden alias shadows", names)
	}
}

func TestDeprecations_replacedBy(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "old", Handler: "AppOld", Deprecated: "renamed", ReplacedBy: "app new",
			Flags: []FlagDef{
				{Name: "out", Identifiers: []string{"--out"}, Type: "string", Deprecated: "renamed", ReplacedBy: "--output"},
				{Name: "format", Identifiers: []string{"--format"}, Type: "string", Enum: []string{"json", "ini"},
					EnumValues: []EnumValue{{Value: "json"}, {Value: "ini", Deprecated: "going away", ReplacedBy: "json"}}},
			},
		}},
	}
	got := Deprecations(NewContextFor(def, []string{"old", "--out", "x", "--format", "ini"}))
	want := []Deprecation{
		{Kind: "command", Name: "old", Identifier: "old", Message: "renamed", ReplacedBy: "app new"},
		{Kind: "flag", Name: "out", Identifier: "--out", Message: "renamed", ReplacedBy: "--output"},
		{Kind: "flag", Name: "format", Identifier: "--format", Message: "going away", ReplacedBy: "json", Value: "ini"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Deprecations =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSecretLiteral_arguments(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "token", Type: "string", Secret: true, From: []string{"stdin"}},
	}}
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				Token string `rotini:"token"`
			}
		}
	}
	err := NewParser().Parse(NewContextFor(def, []string{"sk_live"}), &in)
	if err == nil || err.Error() != "<token> takes -, not a value" {
		t.Errorf("err = %v, want the refusal naming only stdin", err)
	}
}

func TestSecretLiteral_shortCircuitStillRefuses(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
		{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, From: []string{"file"}},
	}}
	var in struct{}
	if err := NewParser().Parse(NewContextFor(def, []string{"--help", "--token", "x"}), &in); err == nil || !strings.Contains(err.Error(), "takes @file") {
		t.Errorf("err = %v, want the refusal under --help", err)
	}
	// Asking what argv says never reads a file, so it never refuses either.
	if got := Deprecations(NewContextFor(def, []string{"--token", "x"})); got != nil {
		t.Errorf("Deprecations = %+v", got)
	}
}

func TestArgvOf_literalFreeSecret(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, From: []string{"file"}},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Token string `rotini:"token"`
			}
			Arguments struct{}
		}
	}
	def.Inputs = reflect.TypeFor[inputs]()
	var v inputs
	v.App.Flags.Token = "sk"
	_, _, err := ArgvOf(def, v, PresenceOf(v), ArgvSecrets())
	if ae, ok := err.(*ArgvError); !ok || !strings.Contains(ae.Reason, "file or stdin") {
		t.Errorf("err = %v, want an *ArgvError asking for a file or stdin", err)
	}
}

func TestFlagDependencies_messages(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
		{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
		{Name: "c", Identifiers: []string{"--c"}, Type: "bool"},
		{Name: "d", Identifiers: []string{"--d"}, Type: "bool", Negatable: true},
	}}
	cases := []struct {
		dep  FlagDependency
		argv []string
		want string
	}{
		{FlagDependency{Unless: []string{"a", "b"}, Requires: []string{"c"}}, nil, "flag --c is required unless --a or --b is set"},
		{FlagDependency{When: "a", Unless: []string{"b"}, Forbids: []string{"c", "d"}}, []string{"--a", "--c", "--d"}, "flags --c and --d can't be used when --a is set, unless --b is set"},
		{FlagDependency{When: "a", Unless: []string{"b"}, Forbids: []string{"c"}}, []string{"--a", "--b", "--c"}, ""},
		{FlagDependency{When: "d", Equals: []string{"false"}, Requires: []string{"c"}}, []string{"--no-d"}, "flag --c is required when --d is false"},
		{FlagDependency{When: "d", Equals: []string{"false"}, Requires: []string{"c"}}, []string{"--d=yes"}, ""},
	}
	for _, c := range cases {
		d := def
		d.FlagDependencies = []FlagDependency{c.dep}
		var in struct{}
		err := NewParser().Parse(NewContextFor(d, c.argv), &in)
		if (c.want == "") != (err == nil) || (err != nil && err.Error() != c.want) {
			t.Errorf("%+v %q: err = %v, want %q", c.dep, c.argv, err, c.want)
		}
	}
}

// TestStructLeaves_embeddedSet pins the walk every input reader shares: promoted fields of an
// embedded struct are visited under their own names, and a struct without one walks as before.
func TestStructLeaves_embeddedSet(t *testing.T) {
	type flags struct {
		All bool `rotini:"all"`
		CpOutputFlagSet
	}
	var got []string
	for sf, f := range structLeaves(reflect.ValueOf(&flags{}).Elem()) {
		if !f.CanSet() {
			t.Errorf("%s is not settable", sf.Name)
		}
		got = append(got, sf.Name)
	}
	if want := []string{"All", "Format", "Quiet"}; !slices.Equal(got, want) {
		t.Errorf("leaves = %v, want %v", got, want)
	}
	var plain []string
	for sf := range typeLeaves(reflect.TypeFor[CpOutputFlagSet]()) {
		plain = append(plain, sf.Name)
	}
	if want := []string{"Format", "Quiet"}; !slices.Equal(plain, want) {
		t.Errorf("leaves = %v, want %v", plain, want)
	}
}

// ArgvOf writes a flag from an embedded flag set like an own flag.
func TestArgvOf_flagSetMember(t *testing.T) {
	def := cpListDef()
	def.Commands[0].Inputs = reflect.TypeFor[cpListInputs]()
	var v cpListInputs
	v.CpList.Flags.Format, v.CpList.Flags.All = "json", true
	argv, _, err := ArgvOf(def, v, PresenceOf(v))
	if want := []string{"list", "--all", "--format=json"}; err != nil || !slices.Equal(argv, want) {
		t.Errorf("ArgvOf = %q, %v; want %q", argv, err, want)
	}
}
