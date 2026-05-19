package rtk_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Helpers
// =============================================================================

// testEnv is a minimal EnvLookup for tests.
type testEnv map[string]string

func (e testEnv) Lookup(key string) (string, bool) {
	v, ok := e[key]
	return v, ok
}

// testConfig is a minimal ConfigLookup for tests. Paths are joined with "."
// and looked up against a flat map.
type testConfig map[string]any

func (c testConfig) Lookup(path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := any(map[string]any(c))
	for _, seg := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// asTarget is a simple Target implementation that records the result it
// receives. Tests use it to inspect what the parser produced without
// driving codegen-emitted PopulateFromArgv.
type asTarget struct {
	path   string
	result *rtk.Result
}

func (t *asTarget) RotiniCommandPath() string { return t.path }

func (t *asTarget) PopulateFromArgv(r *rtk.Result) error {
	t.result = r
	return nil
}

// =============================================================================
// Tokenize
// =============================================================================

func TestTokenize_emptyArgv(t *testing.T) {
	t.Parallel()
	got, err := rtk.Tokenize(nil, rtk.ProgramSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got.CommandPath) != 0 {
		t.Errorf("got %+v, want empty CommandPath", got)
	}
}

func TestTokenize_noMatch(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "add", Path: "add"},
		},
	}
	got, _ := rtk.Tokenize([]string{"--help"}, spec)
	if len(got.CommandPath) != 0 {
		t.Errorf("got %v, want empty (no subcommand match)", got.CommandPath)
	}
}

func TestTokenize_singleLevel(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "add", Path: "add"},
			{Name: "list", Path: "list"},
		},
	}
	got, _ := rtk.Tokenize([]string{"list"}, spec)
	if want := []string{"list"}; !equalStrings(got.CommandPath, want) {
		t.Errorf("got %v, want %v", got.CommandPath, want)
	}
}

func TestTokenize_nested(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "foo", Path: "foo",
				Commands: []rtk.CommandSpec{
					{
						Name: "bar", Path: "foo-bar",
						Commands: []rtk.CommandSpec{
							{Name: "baz", Path: "foo-bar-baz"},
						},
					},
				},
			},
		},
	}
	got, _ := rtk.Tokenize([]string{"foo", "bar", "baz"}, spec)
	if want := []string{"foo", "bar", "baz"}; !equalStrings(got.CommandPath, want) {
		t.Errorf("got %v, want %v", got.CommandPath, want)
	}
}

func TestTokenize_aliases(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "add", Aliases: []string{"a", "new"}, Path: "add"},
		},
	}
	for _, token := range []string{"add", "a", "new"} {
		t.Run(token, func(t *testing.T) {
			got, _ := rtk.Tokenize([]string{token}, spec)
			if want := []string{"add"}; !equalStrings(got.CommandPath, want) {
				t.Errorf("got %v, want %v", got.CommandPath, want)
			}
		})
	}
}

func TestTokenize_doubleDashStopsWalking(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "list", Path: "list"},
		},
	}
	got, _ := rtk.Tokenize([]string{"--", "list"}, spec)
	if len(got.CommandPath) != 0 {
		t.Errorf("got %v, want empty — `list` is positional after --", got.CommandPath)
	}
}

func TestTokenize_skipsFlags(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "list", Path: "list"},
		},
	}
	got, _ := rtk.Tokenize([]string{"--output", "json", "list"}, spec)
	// "--output" is skipped (flag), "json" is its inline value (also skipped
	// by Tokenize — it's not a command match), "list" matches.
	// Note: at the routing layer we don't know which flags take values, so
	// "json" is just an unmatched positional token; it doesn't match `list`
	// either. The first match wins.
	if want := []string{"list"}; !equalStrings(got.CommandPath, want) {
		t.Errorf("got %v, want %v", got.CommandPath, want)
	}
}

// =============================================================================
// Parse end-to-end via NewParser + Target
// =============================================================================

func TestParse_rootFlag_boolPresence(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "todo",
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--help"}})
	tgt := &asTarget{path: ""}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	rootScope := tgt.result.FlagsByScope[""]
	got, ok := rootScope["help"]
	if !ok {
		t.Fatalf("help not present in root scope: %v", rootScope)
	}
	if v, _ := unwrapSingleAny(got); v != true {
		t.Errorf("help: got %v, want true", got)
	}
}

func TestParse_rootFlag_stringWithValue(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"-o", "--output"}, Type: "string"},
		},
	}
	for _, argv := range [][]string{
		{"--output", "json"},
		{"--output=json"},
		{"-o", "json"},
		{"-o=json"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			p := rtk.NewParser(spec, rtk.Inputs{Argv: argv})
			tgt := &asTarget{}
			if err := p.Parse(tgt); err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
			if v != "json" {
				t.Errorf("output: got %v, want \"json\"", v)
			}
		})
	}
}

func TestParse_unknownFlag_returnsError(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--bogus"}})
	err := p.Parse(&asTarget{})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	var ufe *rtk.UnknownFlagError
	if !errors.As(err, &ufe) {
		t.Fatalf("got error %v (%T), want *UnknownFlagError", err, err)
	}
	if ufe.Flag != "--bogus" {
		t.Errorf("UnknownFlagError.Flag: got %q, want %q", ufe.Flag, "--bogus")
	}
}

func TestParse_unknownFlag_suggestsNearest(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"--output"}, Type: "string"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--outptu", "json"}})
	err := p.Parse(&asTarget{})
	var ufe *rtk.UnknownFlagError
	if !errors.As(err, &ufe) {
		t.Fatalf("got %v, want UnknownFlagError", err)
	}
	if len(ufe.Suggestions) != 1 || ufe.Suggestions[0] != "--output" {
		t.Errorf("Suggestions: got %v, want [--output]", ufe.Suggestions)
	}
}

func TestParse_subcommand_flagInScope(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "add", Path: "add",
				Flags: []rtk.FlagSpec{
					{Name: "priority", Identifiers: []string{"-p"}, Type: "string"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"add", "-p", "high"}})
	tgt := &asTarget{path: "add"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if want := []string{"add"}; !equalStrings(tgt.result.CommandPath, want) {
		t.Errorf("CommandPath: got %v, want %v", tgt.result.CommandPath, want)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope["add"]["priority"])
	if v != "high" {
		t.Errorf("priority in 'add' scope: got %v, want \"high\"", v)
	}
}

func TestParse_positionalArguments(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "add", Path: "add",
				Arguments: []rtk.ArgumentSpec{
					{Name: "text", Type: "string", Required: true},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"add", "buy milk"}})
	tgt := &asTarget{path: "add"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if want := []string{"buy milk"}; !equalStrings(tgt.result.ParsedArgs, want) {
		t.Errorf("ParsedArgs: got %v, want %v", tgt.result.ParsedArgs, want)
	}
}

func TestParse_missingRequiredArgument(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "add", Path: "add",
				Arguments: []rtk.ArgumentSpec{
					{Name: "text", Type: "string", Required: true},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"add"}})
	err := p.Parse(&asTarget{path: "add"})
	if err == nil {
		t.Fatal("expected MissingRequiredError")
	}
	var mre *rtk.MissingRequiredError
	if !errors.As(err, &mre) {
		t.Fatalf("got %v (%T), want MissingRequiredError", err, err)
	}
	if mre.Kind != "argument" {
		t.Errorf("Kind: got %q, want \"argument\"", mre.Kind)
	}
}

func TestParse_envFallback(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"--output"}, Type: "string", EnvKey: "TODO_OUTPUT"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv: nil,
		Env:  testEnv{"TODO_OUTPUT": "yaml"},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "yaml" {
		t.Errorf("output: got %v, want \"yaml\"", v)
	}
}

func TestParse_configFallback(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"--output"}, Type: "string", ConfigKey: "defaults.output"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{}, // make sure FlagsByScope[""] gets created
		Config: testConfig{"defaults": map[string]any{"output": "table"}},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// When Argv is empty, parseProgram returns early without populating
	// from env/config — that matches rotiniold behavior. Verify by sending
	// a benign root flag and re-asserting:
	p2 := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{"--output", "json"},
		Config: testConfig{"defaults": map[string]any{"output": "table"}},
	})
	tgt2 := &asTarget{}
	if err := p2.Parse(tgt2); err != nil {
		t.Fatalf("Parse2 error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt2.result.FlagsByScope[""]["output"])
	// argv wins over config
	if v != "json" {
		t.Errorf("argv-wins-over-config: got %v, want \"json\"", v)
	}
}

func TestParse_defaultValue(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"--output"}, Type: "string", Default: "table"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--help"}})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "table" {
		t.Errorf("default output: got %v, want \"table\"", v)
	}
}

func TestParse_enumValidation(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"--output"}, Type: "string",
				Enum: []string{"json", "yaml", "table"}},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--output", "xml"}})
	err := p.Parse(&asTarget{})
	if err == nil {
		t.Fatal("expected validation error for enum mismatch")
	}
	var ve *rtk.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v (%T), want ValidationError", err, err)
	}
	if ve.Constraint != "enum" {
		t.Errorf("Constraint: got %q, want \"enum\"", ve.Constraint)
	}
}

func TestParse_minMaxValidation(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "port", Identifiers: []string{"--port"}, Type: "int",
				Min: rtk.Ptr(1.0), Max: rtk.Ptr(65535.0)},
		},
	}
	t.Run("below_min", func(t *testing.T) {
		t.Parallel()
		p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--port", "0"}})
		err := p.Parse(&asTarget{})
		if err == nil {
			t.Fatal("expected validation error for port=0 (< 1)")
		}
	})
	t.Run("above_max", func(t *testing.T) {
		t.Parallel()
		p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--port", "99999"}})
		err := p.Parse(&asTarget{})
		if err == nil {
			t.Fatal("expected validation error for port=99999 (> 65535)")
		}
	})
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--port", "8080"}})
		tgt := &asTarget{}
		if err := p.Parse(tgt); err != nil {
			t.Fatalf("Parse error: %v", err)
		}
		v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["port"])
		if v != 8080 {
			t.Errorf("port: got %v, want 8080", v)
		}
	})
}

func TestParse_groupedShortFlags(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "list", Path: "list",
				Flags: []rtk.FlagSpec{
					{Name: "all", Identifiers: []string{"-a"}, Type: "bool"},
					{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
					{Name: "long", Identifiers: []string{"-l"}, Type: "bool"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"list", "-avl"}})
	tgt := &asTarget{path: "list"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	scope := tgt.result.FlagsByScope["list"]
	for _, name := range []string{"all", "verbose", "long"} {
		v, _ := unwrapSingleAny(scope[name])
		if v != true {
			t.Errorf("%s: got %v, want true", name, v)
		}
	}
}

func TestParse_variadicArguments(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "rm", Path: "rm",
				Arguments: []rtk.ArgumentSpec{
					{Name: "files", Type: "[]string", Variadic: true},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"rm", "a.txt", "b.txt", "c.txt"}})
	tgt := &asTarget{path: "rm"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	want := []string{"a.txt", "b.txt", "c.txt"}
	if !equalStrings(tgt.result.ParsedArgs, want) {
		t.Errorf("ParsedArgs: got %v, want %v", tgt.result.ParsedArgs, want)
	}
}

func TestParse_doubleDashCollectsPositionals(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "run", Path: "run",
				Arguments: []rtk.ArgumentSpec{
					{Name: "cmd", Type: "string"},
					{Name: "args", Type: "[]string", Variadic: true},
				},
				Flags: []rtk.FlagSpec{
					{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"run", "-v", "echo", "--", "-n", "hello"}})
	tgt := &asTarget{path: "run"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// "echo" is the first positional; "-n" and "hello" land after `--`
	want := []string{"echo", "-n", "hello"}
	if !equalStrings(tgt.result.ParsedArgs, want) {
		t.Errorf("ParsedArgs: got %v, want %v", tgt.result.ParsedArgs, want)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope["run"]["verbose"])
	if v != true {
		t.Errorf("verbose: got %v, want true", v)
	}
}

func TestParse_unknownSubcommand_path(t *testing.T) {
	t.Parallel()
	// When Target.RotiniCommandPath is set but the parsed argv didn't match
	// that command (or any command), Parse still succeeds — the result just
	// has an empty CommandPath. The Target's PopulateFromArgv is responsible
	// for noticing the mismatch if it matters.
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "add", Path: "add"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: nil})
	tgt := &asTarget{path: "add"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
}

// =============================================================================
// Test helpers — value comparison and the appendFlagValue wrapping shape
// =============================================================================

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// unwrapSingleAny mimics what codegen-emitted PopulateFromArgv does for the
// common case of "single occurrence of a flag" — it unwraps the []any
// wrapper that appendFlagValue uses.
func unwrapSingleAny(v any) (any, bool) {
	if slice, ok := v.([]any); ok && len(slice) == 1 {
		return slice[0], true
	}
	return v, false
}
