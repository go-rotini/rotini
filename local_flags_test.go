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
		{[]string{"db", "migrate", "--init"}, `--init belongs to "app", not "app db migrate"`},
		{[]string{"--init", "db", "migrate"}, `--init belongs to "app", not "app db migrate"`},
		{[]string{"db", "--dsn=x", "migrate"}, `--dsn belongs to "app db", not "app db migrate"`},
		{[]string{"db", "migrate", "--dsn", "x"}, `--dsn belongs to "app db", not "app db migrate"`},
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
		if !errors.As(err, &pe) || pe.Kind != ParseKindMisplacedFlag || pe.Msg != tc.want {
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
