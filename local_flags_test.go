package rotini

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// localDef has a root with a local --init and an inherited --verbose, and a db → migrate
// branch whose db declares a local --dsn.
func localDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "init", Identifiers: []string{"--init"}, Type: "bool", Local: true},
			{Name: "verbose", Identifiers: []string{"--verbose", "-v"}, Type: "bool"},
		},
		Commands: []CommandDef{{
			Name: "db", Handler: "AppDb",
			Flags: []FlagDef{{Name: "dsn", Identifiers: []string{"--dsn"}, Type: "string", Local: true}},
			Commands: []CommandDef{{
				Name: "migrate", Handler: "AppDbMigrate",
				Flags: []FlagDef{{Name: "steps", Identifiers: []string{"--steps"}, Type: "int"}},
			}},
		}},
	}
}

func TestLocalFlag_parse(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string // "" = parses
	}{
		{[]string{"--init"}, ""},
		{[]string{"db", "--dsn", "x"}, ""},
		{[]string{"db", "migrate", "--verbose"}, ""},
		{[]string{"-v", "db", "migrate", "--steps", "2"}, ""},
		{[]string{"db", "migrate", "--init"}, `app db migrate: --init belongs to "app"`},
		{[]string{"--init", "db", "migrate"}, `app db migrate: --init belongs to "app"`},
		{[]string{"db", "--dsn=x", "migrate"}, `app db migrate: --dsn belongs to "app db"`},
		{[]string{"db", "migrate", "--dsn", "x"}, `app db migrate: --dsn belongs to "app db"`},
	} {
		rtx := NewContextFor(localDef(), tc.argv)
		_, err := parseInto(rtx.CommandChain(), tc.argv, rtx.argvAcq())
		if tc.want == "" {
			if err != nil {
				t.Errorf("%q: %v, want it to parse", tc.argv, err)
			}
			continue
		}
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Kind != ParseKindMisplacedFlag || err.Error() != tc.want {
			t.Errorf("%q: %v, want a misplaced-flag error %q", tc.argv, err, tc.want)
		}
		if CategoryOf(err) != CategoryUsage {
			t.Errorf("%q: category %v, want usage", tc.argv, CategoryOf(err))
		}
	}
}

// A flag without Local keeps being accepted after its sub-commands' names, so a program
// generated before Local existed runs unchanged.
func TestLocalFlag_unmarkedStillInherited(t *testing.T) {
	def := localDef()
	def.Flags[0].Local = false
	argv := []string{"db", "migrate", "--init"}
	rtx := NewContextFor(def, argv)
	if _, err := parseInto(rtx.CommandChain(), argv, rtx.argvAcq()); err != nil {
		t.Fatal(err)
	}
}

func TestLocalFlag_unknownFlagCandidates(t *testing.T) {
	argv := []string{"db", "migrate", "--nope"}
	rtx := NewContextFor(localDef(), argv)
	_, err := parseInto(rtx.CommandChain(), argv, rtx.argvAcq())
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Kind != ParseKindUnknownFlag {
		t.Fatalf("err = %v, want an unknown flag", err)
	}
	for _, id := range []string{"--init", "--dsn"} {
		if slices.Contains(pe.Candidates, id) {
			t.Errorf("candidates %q offer %s, which the run would reject", pe.Candidates, id)
		}
	}
	if !slices.Contains(pe.Candidates, "--verbose") || !slices.Contains(pe.Candidates, "--steps") {
		t.Errorf("candidates %q, want --verbose and --steps", pe.Candidates)
	}
}

func TestLocalFlag_completion(t *testing.T) {
	for _, tc := range []struct {
		words   []string
		has     []string
		hasNot  []string
		message string
	}{
		{[]string{"--"}, []string{"--init", "--verbose"}, nil, "the root offers its own local flag"},
		{[]string{"db", "--"}, []string{"--dsn", "--verbose"}, []string{"--init"}, "db offers its own, not the root's local flag"},
		{[]string{"db", "migrate", "--"}, []string{"--steps", "--verbose"}, []string{"--init", "--dsn"}, "migrate offers no ancestor's local flag"},
	} {
		got := complete(localDef(), tc.words, nil, nil)
		for _, id := range tc.has {
			if !slices.Contains(got, id) {
				t.Errorf("%s: %q lacks %s", tc.message, got, id)
			}
		}
		for _, id := range tc.hasNot {
			if slices.Contains(got, id) {
				t.Errorf("%s: %q offers %s", tc.message, got, id)
			}
		}
	}
	if got := complete(localDef(), []string{"db", "migrate", "--dsn="}, nil, nil); len(got) != 0 {
		t.Errorf("--dsn= below db = %q, want nothing", got)
	}
}

func TestLocalFlag_argvOf(t *testing.T) {
	type in struct {
		App struct {
			Flags struct {
				Init    bool `rotini:"init"`
				Verbose bool `rotini:"verbose"`
			}
		}
		AppDb struct {
			Flags struct {
				Dsn string `rotini:"dsn"`
			}
		}
		AppDbMigrate struct {
			Flags struct {
				Steps int `rotini:"steps"`
			}
		}
	}
	var v in
	v.App.Flags.Verbose = true
	v.AppDbMigrate.Flags.Steps = 2
	argv, _, err := ArgvOf(localDef(), v, PresenceOf(v), ArgvPath("db", "migrate"))
	if err != nil || !slices.Equal(argv, []string{"--verbose", "db", "migrate", "--steps=2"}) {
		t.Fatalf("argv %q, err %v", argv, err)
	}

	v.AppDb.Flags.Dsn = "x"
	_, _, err = ArgvOf(localDef(), v, PresenceOf(v), ArgvPath("db", "migrate"))
	var ae *ArgvError
	if !errors.As(err, &ae) || ae.Field != "AppDb.Flags.Dsn" || !strings.Contains(ae.Reason, `"db" is the command run`) {
		t.Fatalf("err = %v, want an ArgvError for AppDb.Flags.Dsn", err)
	}
}

// An ancestor's local flag gives no completion hint under a sub-command, inline or not.
func TestLocalFlag_completionHint(t *testing.T) {
	def := localDef()
	def.Commands[0].Flags[0].Complete = Completion{Kind: "file"}
	p := NewProgram(def, nil)
	if got := resultOf(t, p, "db", "--dsn="); got.Hint.Kind != "file" {
		t.Errorf("--dsn= on db: hint %q, want file", got.Hint.Kind)
	}
	if got := resultOf(t, p, "db", "migrate", "--dsn="); got.Hint.Kind == "file" {
		t.Errorf("--dsn= under migrate: hint %q, want none", got.Hint.Kind)
	}
}

// localRulesDef has a root whose local flags carry every presence rule (required, groups and
// dependencies) above a sub-command, plus a cascading --level the rules also name.
func localRulesDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true, Local: true},
			{Name: "a", Identifiers: []string{"--a"}, Type: "bool", Local: true},
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool", Local: true},
			{Name: "c", Identifiers: []string{"--c"}, Type: "bool", Local: true},
			{Name: "x", Identifiers: []string{"--x"}, Type: "bool"},
			{Name: "y", Identifiers: []string{"--y"}, Type: "bool"},
			{Name: "level", Identifiers: []string{"--level"}, Type: "string"},
		},
		FlagGroups: []FlagGroup{
			{Kind: FlagGroupOneOf, Flags: []string{"a", "b"}},
			{Kind: FlagGroupMutuallyExclusive, Flags: []string{"c", "x", "y"}},
		},
		FlagDependencies: []FlagDependency{{When: "level", Requires: []string{"c"}}},
		Commands:         []CommandDef{{Name: "sub", Handler: "AppSub"}},
	}
}

type localRulesApp struct {
	Flags struct {
		Token string `rotini:"token"`
		A     bool   `rotini:"a"`
		B     bool   `rotini:"b"`
		C     bool   `rotini:"c"`
		X     bool   `rotini:"x"`
		Y     bool   `rotini:"y"`
		Level string `rotini:"level"`
	}
}

type localRulesRoot struct{ App localRulesApp }

type localRulesSub struct {
	App    localRulesApp
	AppSub struct{}
}

// A local flag's required, group and dependency rules apply only when its command is run: under
// a sub-command, where the flag can't be given, they are left out, and every reader agrees.
func TestLocalFlag_rulesOnlyWhenRun(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string // "" = valid
	}{
		{[]string{"sub"}, ""},
		{[]string{"--level", "v", "sub"}, ""},
		{[]string{"sub", "--x", "--y"}, "mutually exclusive"},
		{[]string{}, "missing required input: --token"},
		{[]string{"--token", "t"}, "exactly one of --a, --b is required"},
		{[]string{"--token", "t", "--a", "--level", "v"}, "flag --c is required when --level is set"},
		{[]string{"--token", "t", "--a", "--level", "v", "--c"}, ""},
	} {
		rtx := NewContextFor(localRulesDef(), tc.argv)
		if len(tc.argv) > 0 && slices.Contains(tc.argv, "sub") {
			checkLocalRules[localRulesSub](t, rtx, tc.argv, tc.want)
		} else {
			checkLocalRules[localRulesRoot](t, rtx, tc.argv, tc.want)
		}
	}
}

func checkLocalRules[T any](t *testing.T, rtx *Context, argv []string, want string) {
	t.Helper()
	check := func(api string, err error) {
		t.Helper()
		switch {
		case want == "" && err != nil:
			t.Errorf("%s %q: %v, want valid", api, argv, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s %q: %v, want %q", api, argv, err, want)
		}
	}
	_, err := rtx.Inputs[T]()
	check("Inputs", err)
	in, rep, err := rtx.InputsWithReport[T]()
	check("InputsWithReport", err)
	if err == nil {
		check("InputReport.Validate", rep.Validate())
	}
	check("CheckInputs", rtx.CheckInputs(in, PresenceOf(in)))
}

// Completion doesn't list an ancestor's local flag as required, nor a cascading flag because a
// group or dependency names a local one.
func TestLocalFlag_completionRequiredFirst(t *testing.T) {
	def := localRulesDef()
	def.FlagGroups = []FlagGroup{{Kind: FlagGroupAtLeastOne, Flags: []string{"a", "y"}}}
	def.FlagDependencies = []FlagDependency{{When: "a", Requires: []string{"x"}}}
	got := complete(def, []string{"sub", "--"}, nil, nil)
	want := []string{"--level", "--x", "--y"}
	if !slices.Equal(got, want) {
		t.Errorf("sub --: %q, want %q in plain order", got, want)
	}
	if got := complete(def, []string{"--"}, nil, nil); len(got) == 0 || got[0] != "--a" {
		t.Errorf("root --: %q, want the required flags first", got)
	}
}
