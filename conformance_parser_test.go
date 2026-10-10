package rotini

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Conformance cases for flag sets, value-conditional dependencies, literal-free secrets and
// hidden identifiers, each on its own small fixture in the shapes codegen emits.

// CpOutputFlagSet is a flag set's struct, embedded in a command's flags as codegen does.
type CpOutputFlagSet struct {
	Format string `rotini:"format" recon:"format" env:"CP_FORMAT"`
	Quiet  bool   `rotini:"quiet"`
}

type cpListInputs struct {
	CpList struct {
		Flags struct {
			All bool `rotini:"all"`
			CpOutputFlagSet
		}
		Arguments struct{}
	}
}

func cpListDef() Definition {
	return Definition{Name: "cp", Handler: "Cp", Commands: []CommandDef{{
		Name: "list", Handler: "CpList",
		Flags: []FlagDef{
			{Name: "all", Identifiers: []string{"--all"}, Type: "bool"},
			{Name: "format", Identifiers: []string{"--format"}, Type: "string", Enum: []string{"text", "json"}, Default: "text"},
			{Name: "quiet", Identifiers: []string{"--quiet"}, Type: "bool"},
		},
		FlagGroups: []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"format", "quiet"}}},
	}}}
}

func readCpList(env []string, argv ...string) (cpListInputs, error) {
	return NewContextFor(cpListDef(), append([]string{"list"}, argv...)).WithEnviron(env).Inputs[cpListInputs]()
}

// FLAG-24: a flag from a flag set, embedded in the command's flags struct, binds, falls back,
// defaults and validates like an own flag, and its provenance path is the promoted one.
func confFlagSetMember(t *testing.T, _ *Context, _ InputSettings) {
	if in, err := readCpList(nil, "--format", "json", "--all"); err != nil || in.CpList.Flags.Format != "json" || !in.CpList.Flags.All {
		t.Errorf("argv: %+v, %v", in.CpList.Flags, err)
	}
	if in, err := readCpList([]string{"CP_FORMAT=json"}); err != nil || in.CpList.Flags.Format != "json" {
		t.Errorf("env fallback: %+v, %v", in.CpList.Flags, err)
	}
	if in, err := readCpList(nil); err != nil || in.CpList.Flags.Format != "text" {
		t.Errorf("default: %+v, %v", in.CpList.Flags, err)
	}
	if _, err := readCpList(nil, "--format", "xml"); err == nil || !strings.Contains(err.Error(), `invalid value "xml" for --format`) {
		t.Errorf("enum: err = %v", err)
	}
	if _, err := readCpList(nil, "--format", "json", "--quiet"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("group: err = %v", err)
	}
	_, report, err := NewContextFor(cpListDef(), []string{"list", "--format", "json"}).InputsWithReport[cpListInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := report.Winner("CpList.Flags.Format"); !ok || w.Layer != "argv" {
		t.Errorf("Winner(CpList.Flags.Format) = %+v, %v; want argv", w, ok)
	}
	var typed cpListInputs
	typed.CpList.Flags.Format, typed.CpList.Flags.Quiet = "json", true
	if err := NewContextFor(cpListDef(), []string{"list"}).CheckInputs(typed, PresenceOf(typed)); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("CheckInputs on the embedded set: err = %v", err)
	}
}

// FLAG-25: a hidden identifier parses wherever a listed one does, long, short in a bundle,
// negated and with =value, and is never offered as a candidate.
func confHiddenIdentifier(t *testing.T, _ *Context, _ InputSettings) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "output", Identifiers: []string{"--output"}, HiddenIdentifiers: []string{"--out", "-O"}, Type: "string"},
		{Name: "color", Identifiers: []string{"--color"}, HiddenIdentifiers: []string{"--colour"}, Type: "bool", Negatable: true, Default: "true"},
		{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Output  string `rotini:"output"`
				Color   bool   `rotini:"color"`
				Verbose bool   `rotini:"verbose"`
			}
			Arguments struct{}
		}
	}
	for _, argv := range [][]string{{"--out", "x"}, {"--out=x"}, {"-vO", "x"}} {
		in, err := NewContextFor(def, argv).Inputs[inputs]()
		if err != nil || in.App.Flags.Output != "x" {
			t.Errorf("%q: %+v, %v", argv, in.App.Flags, err)
		}
	}
	if in, err := NewContextFor(def, []string{"--no-colour"}).Inputs[inputs](); err != nil || in.App.Flags.Color {
		t.Errorf("--no-colour: %+v, %v", in.App.Flags, err)
	}
	_, err := NewContextFor(def, []string{"--outt"}).Inputs[inputs]()
	_, cands, ok := SuggestionFacts(err)
	if !ok || slices.Contains(cands, "--out") || slices.Contains(cands, "--no-colour") || !slices.Contains(cands, "--output") {
		t.Errorf("candidates = %q, %v; want the listed spellings only", cands, ok)
	}
}

func depDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "format", Identifiers: []string{"--format"}, Type: "string", Enum: []string{"text", "csv"}, IgnoreCase: true},
		{Name: "delimiter", Identifiers: []string{"--delimiter"}, Type: "string"},
		{Name: "all", Identifiers: []string{"--all"}, Type: "bool"},
		{Name: "limit", Identifiers: []string{"--limit"}, Type: "int"},
		{Name: "config", Identifiers: []string{"--config"}, Type: "string"},
		{Name: "token", Identifiers: []string{"--token"}, Type: "string"},
	}, FlagDependencies: []FlagDependency{
		{When: "format", Equals: []string{"csv"}, Requires: []string{"delimiter"}},
		{When: "all", Forbids: []string{"limit"}},
		{Unless: []string{"config"}, Requires: []string{"token"}},
	}}
}

type depInputs struct {
	App struct {
		Flags struct {
			Format    string `rotini:"format" recon:"format" env:"APP_FORMAT"`
			Delimiter string `rotini:"delimiter"`
			All       bool   `rotini:"all"`
			Limit     int    `rotini:"limit"`
			Config    string `rotini:"config"`
			Token     string `rotini:"token"`
		}
		Arguments struct{}
	}
}

func readDep(env []string, argv ...string) error {
	_, err := NewContextFor(depDef(), argv).WithEnviron(env).Inputs[depInputs]()
	return err
}

// wantDep checks that err is nil (want "") or a usage error containing want.
func wantDep(t *testing.T, label string, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("%s: err = %v, want none", label, err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want) || CategoryOf(err) != CategoryUsage):
		t.Errorf("%s: err = %v, want a usage error containing %q", label, err, want)
	}
}

// DEP-01: a dependency with equals triggers on the flag's value, compared as the enum stores
// it, so ignore_case applies.
func confDependencyEquals(t *testing.T, _ *Context, _ InputSettings) {
	wantDep(t, "csv", readDep(nil, "--token", "t", "--format", "CSV"), "flag --delimiter is required when --format is csv")
	wantDep(t, "csv with delimiter", readDep(nil, "--token", "t", "--format", "csv", "--delimiter", ";"), "")
	wantDep(t, "text", readDep(nil, "--token", "t", "--format", "text"), "")
}

// DEP-02: only the command line triggers a dependency; a value from the environment doesn't.
func confDependencyArgvOnly(t *testing.T, _ *Context, _ InputSettings) {
	wantDep(t, "env", readDep([]string{"APP_FORMAT=csv"}, "--token", "t"), "")
}

// DEP-03: forbids rejects a flag set together with the trigger.
func confDependencyForbids(t *testing.T, _ *Context, _ InputSettings) {
	wantDep(t, "all and limit", readDep(nil, "--token", "t", "--all", "--limit", "5"), "flag --limit can't be used when --all is set")
	wantDep(t, "limit alone", readDep(nil, "--token", "t", "--limit", "5"), "")
}

// DEP-04: a rule with only unless applies on every run unless one of its flags is set.
func confDependencyUnless(t *testing.T, _ *Context, _ InputSettings) {
	wantDep(t, "neither", readDep(nil), "flag --token is required unless --config is set")
	wantDep(t, "config", readDep(nil, "--config", "c"), "")
	wantDep(t, "token", readDep(nil, "--token", "t"), "")
}

// DEP-05: CheckInputs compares the typed value, and a short-circuit flag waives the rules.
func confDependencyTyped(t *testing.T, _ *Context, _ InputSettings) {
	var v depInputs
	v.App.Flags.Format, v.App.Flags.Token = "csv", "t"
	wantDep(t, "typed csv", NewContextFor(depDef(), nil).CheckInputs(v, PresenceOf(v)), "flag --delimiter is required when --format is csv")
	v.App.Flags.Format = "text"
	wantDep(t, "typed text", NewContextFor(depDef(), nil).CheckInputs(v, PresenceOf(v)), "")
}

// SEC-04: a secret flag whose from leaves out value refuses a literal on the command line,
// without echoing it, and still reads @file and -.
func confSecretLiteralFree(t *testing.T, _ *Context, _ InputSettings) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("sk_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, From: []string{"file", "stdin"}},
		{Name: "key", Identifiers: []string{"--key"}, Type: "string", Secret: true, From: []string{"value", "file"}},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Token string `rotini:"token"`
				Key   string `rotini:"key"`
			}
			Arguments struct{}
		}
	}
	read := func(argv ...string) (inputs, error) {
		return NewContextFor(def, argv).WithDir(dir).Inputs[inputs]()
	}
	for _, argv := range [][]string{{"--token", "sk_live"}, {"--token=sk_live"}, {"--token="}, {"--token", "@@sk_live"}} {
		_, err := read(argv...)
		if err == nil || !strings.Contains(err.Error(), "--token takes @file or -, not a value") || strings.Contains(err.Error(), "sk_live") || CategoryOf(err) != CategoryUsage {
			t.Errorf("%q: err = %v, want the refusal without the value", argv, err)
		}
	}
	if in, err := read("--token", "@token.txt"); err != nil || in.App.Flags.Token != "sk_file" {
		t.Errorf("@file: %+v, %v", in.App.Flags, err)
	}
	if in, err := read("--key", "literal"); err != nil || in.App.Flags.Key != "literal" {
		t.Errorf("from lists value: %+v, %v", in.App.Flags, err)
	}
}
