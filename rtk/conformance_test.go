package rtk_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
	"github.com/go-rotini/yaml"
)

// =============================================================================
// Conformance suite
//
// Each fixture under rtk/testdata/conformance/*.yaml declares a self-contained
// parser scenario — a ProgramSpec, a set of Inputs, and the expected Result
// (or error). The driver translates the fixture into rtk types, runs Parse,
// and compares the produced Result against the declaration.
//
// The fixtures collectively exercise the parser's full surface: flag styles,
// argument shapes, precedence (argv > env > config > default), stdin shaping,
// validation errors, and structural errors. They serve as the regression
// safety net replacing the line-by-line port verification of rotiniold.
// =============================================================================

// fixture is the on-disk shape of a conformance test case.
type fixture struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Spec        fixtureSpec      `yaml:"spec"`
	Inputs      fixtureInputs    `yaml:"inputs"`
	Expected    *fixtureExpected `yaml:"expected"`
}

type fixtureSpec struct {
	Name      string           `yaml:"name"`
	Flags     []fixtureFlag    `yaml:"flags"`
	Commands  []fixtureCommand `yaml:"commands"`
	RootStdin *fixtureStdin    `yaml:"root_stdin"`
}

type fixtureCommand struct {
	Name      string            `yaml:"name"`
	Path      string            `yaml:"path"`
	Aliases   []string          `yaml:"aliases"`
	Flags     []fixtureFlag     `yaml:"flags"`
	Arguments []fixtureArgument `yaml:"arguments"`
	Stdin     *fixtureStdin     `yaml:"stdin"`
	Commands  []fixtureCommand  `yaml:"commands"`
}

type fixtureFlag struct {
	Name        string   `yaml:"name"`
	Identifiers []string `yaml:"identifiers"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Enum        []string `yaml:"enum"`
	Pattern     string   `yaml:"pattern"`
	Min         *float64 `yaml:"min"`
	Max         *float64 `yaml:"max"`
	MinLength   *int     `yaml:"min_length"`
	MaxLength   *int     `yaml:"max_length"`
	EnvKey      string   `yaml:"env_key"`
	ConfigKey   string   `yaml:"config_key"`
}

type fixtureArgument struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Required  bool     `yaml:"required"`
	Variadic  bool     `yaml:"variadic"`
	Default   string   `yaml:"default"`
	Enum      []string `yaml:"enum"`
	Pattern   string   `yaml:"pattern"`
	Min       *float64 `yaml:"min"`
	Max       *float64 `yaml:"max"`
	MinLength *int     `yaml:"min_length"`
	MaxLength *int     `yaml:"max_length"`
}

type fixtureStdin struct {
	Format string `yaml:"format"`
}

type fixtureInputs struct {
	Argv   []string          `yaml:"argv"`
	Env    map[string]string `yaml:"env"`
	Config map[string]any    `yaml:"config"`
	Stdin  string            `yaml:"stdin"`
}

type fixtureExpected struct {
	// CommandPath is the matched command path. nil and [] are equivalent.
	CommandPath []string `yaml:"command_path"`

	// ParsedArgs are the positional arguments collected at the leaf.
	ParsedArgs []string `yaml:"parsed_args"`

	// FlagsByScope maps scope name → flag name → expected value. Values
	// are compared after unwrapping the single-element []any envelope that
	// appendFlagValue uses; lists in the fixture compare against the
	// raw []any (for repeated-flag cases).
	FlagsByScope map[string]map[string]any `yaml:"flags_by_scope"`

	// Stdin is the expected Result.Stdin value (string for "text", []byte
	// for "raw" via base64, map[string]any for structured formats). YAML's
	// native types map cleanly except for []byte; raw stdin tests state the
	// expected bytes as a string and we compare via []byte conversion when
	// the actual is []byte.
	Stdin any `yaml:"stdin"`

	// ErrorContains, when non-empty, asserts that Parse returned an error
	// whose Error() contains this substring. When set, the other fields
	// are not checked.
	ErrorContains string `yaml:"error_contains"`
}

func TestConformance(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("testdata", "conformance")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("conformance: read dir %s: %v", dir, err)
	}

	var fixtureFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		fixtureFiles = append(fixtureFiles, filepath.Join(dir, e.Name()))
	}
	if len(fixtureFiles) == 0 {
		t.Fatalf("conformance: no fixtures found under %s", dir)
	}
	sort.Strings(fixtureFiles)

	for _, path := range fixtureFiles {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			runConformanceFixture(t, path)
		})
	}
}

func runConformanceFixture(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixture
	if err := yaml.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	spec := translateSpec(fx.Spec)
	inputs := translateInputs(fx.Inputs)

	// Identify the target command path. The driver supports a single leaf
	// per fixture — declare the path on the expected block.
	var leafPath string
	if fx.Expected != nil && len(fx.Expected.CommandPath) > 0 {
		leafPath = strings.Join(fx.Expected.CommandPath, "-")
	}

	target := &asTarget{path: leafPath}
	parseErr := rtk.NewParser(spec, inputs).Parse(target)

	if fx.Expected == nil {
		t.Fatalf("fixture %q: missing `expected` block", fx.Name)
	}

	if fx.Expected.ErrorContains != "" {
		if parseErr == nil {
			t.Fatalf("fixture %q: expected error containing %q, got nil",
				fx.Name, fx.Expected.ErrorContains)
		}
		if !strings.Contains(parseErr.Error(), fx.Expected.ErrorContains) {
			t.Errorf("fixture %q: error %q does not contain %q",
				fx.Name, parseErr.Error(), fx.Expected.ErrorContains)
		}
		return
	}

	if parseErr != nil {
		t.Fatalf("fixture %q: unexpected error: %v", fx.Name, parseErr)
	}
	if target.result == nil {
		t.Fatalf("fixture %q: parse succeeded but target.result is nil", fx.Name)
	}
	got := target.result

	if !equalStringSlices(got.CommandPath, fx.Expected.CommandPath) {
		t.Errorf("fixture %q: CommandPath: got %v, want %v",
			fx.Name, got.CommandPath, fx.Expected.CommandPath)
	}

	if !equalStringSlices(got.ParsedArgs, fx.Expected.ParsedArgs) {
		t.Errorf("fixture %q: ParsedArgs: got %v, want %v",
			fx.Name, got.ParsedArgs, fx.Expected.ParsedArgs)
	}

	// Compare flag scopes. Empty fixture scope means we still verify that
	// no unexpected flags landed at that scope.
	for scope, wantFlags := range fx.Expected.FlagsByScope {
		gotScope, ok := got.FlagsByScope[scope]
		if !ok && len(wantFlags) > 0 {
			t.Errorf("fixture %q: scope %q absent from result", fx.Name, scope)
			continue
		}
		for flagName, wantVal := range wantFlags {
			gotRaw, present := gotScope[flagName]
			if !present {
				t.Errorf("fixture %q: scope %q flag %q absent from result",
					fx.Name, scope, flagName)
				continue
			}
			if !flagValueEqual(gotRaw, wantVal) {
				t.Errorf("fixture %q: scope %q flag %q: got %v (%T), want %v (%T)",
					fx.Name, scope, flagName, gotRaw, gotRaw, wantVal, wantVal)
			}
		}
		// Surface unexpected flags (anti-regression for accidental writes).
		for flagName := range gotScope {
			if _, declared := wantFlags[flagName]; !declared {
				t.Errorf("fixture %q: scope %q flag %q present in result but not expected (got %v)",
					fx.Name, scope, flagName, gotScope[flagName])
			}
		}
	}
	// Surface scopes that the parser produced but the fixture didn't
	// declare (catch accidental scope writes during nested-command refactors).
	for scope := range got.FlagsByScope {
		if _, declared := fx.Expected.FlagsByScope[scope]; declared {
			continue
		}
		if len(got.FlagsByScope[scope]) > 0 {
			t.Errorf("fixture %q: scope %q present in result but not in expected.flags_by_scope (got %v)",
				fx.Name, scope, got.FlagsByScope[scope])
		}
	}

	if !stdinEqual(got.Stdin, fx.Expected.Stdin) {
		t.Errorf("fixture %q: Stdin: got %v (%T), want %v (%T)",
			fx.Name, got.Stdin, got.Stdin, fx.Expected.Stdin, fx.Expected.Stdin)
	}
}

// =============================================================================
// Translation: fixture types → rtk spec types
// =============================================================================

func translateSpec(s fixtureSpec) rtk.ProgramSpec {
	out := rtk.ProgramSpec{
		Name:  s.Name,
		Flags: translateFlags(s.Flags),
	}
	if s.RootStdin != nil {
		out.RootStdin = &rtk.StdinSpec{Format: s.RootStdin.Format}
	}
	out.Commands = translateCommands(s.Commands)
	return out
}

func translateCommands(cmds []fixtureCommand) []rtk.CommandSpec {
	if len(cmds) == 0 {
		return nil
	}
	out := make([]rtk.CommandSpec, len(cmds))
	for i, c := range cmds {
		out[i] = rtk.CommandSpec{
			Name:      c.Name,
			Path:      c.Path,
			Aliases:   c.Aliases,
			Flags:     translateFlags(c.Flags),
			Arguments: translateArguments(c.Arguments),
			Commands:  translateCommands(c.Commands),
		}
		if c.Stdin != nil {
			out[i].Stdin = &rtk.StdinSpec{Format: c.Stdin.Format}
		}
	}
	return out
}

func translateFlags(flags []fixtureFlag) []rtk.FlagSpec {
	if len(flags) == 0 {
		return nil
	}
	out := make([]rtk.FlagSpec, len(flags))
	for i, f := range flags {
		out[i] = rtk.FlagSpec{
			Name:        f.Name,
			Identifiers: f.Identifiers,
			Type:        f.Type,
			Required:    f.Required,
			Default:     f.Default,
			Enum:        f.Enum,
			Pattern:     f.Pattern,
			Min:         f.Min,
			Max:         f.Max,
			MinLength:   f.MinLength,
			MaxLength:   f.MaxLength,
			EnvKey:      f.EnvKey,
			ConfigKey:   f.ConfigKey,
		}
	}
	return out
}

func translateArguments(args []fixtureArgument) []rtk.ArgumentSpec {
	if len(args) == 0 {
		return nil
	}
	out := make([]rtk.ArgumentSpec, len(args))
	for i, a := range args {
		out[i] = rtk.ArgumentSpec{
			Name:      a.Name,
			Type:      a.Type,
			Required:  a.Required,
			Variadic:  a.Variadic,
			Default:   a.Default,
			Enum:      a.Enum,
			Pattern:   a.Pattern,
			Min:       a.Min,
			Max:       a.Max,
			MinLength: a.MinLength,
			MaxLength: a.MaxLength,
		}
	}
	return out
}

func translateInputs(in fixtureInputs) rtk.Inputs {
	out := rtk.Inputs{Argv: in.Argv}
	if len(in.Env) > 0 {
		out.Env = testEnv(in.Env)
	}
	if len(in.Config) > 0 {
		out.Config = testConfig(in.Config)
	}
	if in.Stdin != "" {
		out.Stdin = strings.NewReader(in.Stdin)
	}
	return out
}

// =============================================================================
// Comparison helpers
// =============================================================================

func equalStringSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// flagValueEqual compares a flag value (from FlagsByScope, in raw []any-
// wrapped form) against a fixture-declared expected value. The fixture
// states the value in its "natural" form (e.g., "json" for a single-set
// string flag). For repeated flags, the fixture states a list — the actual
// is compared as a []any against the expected list.
func flagValueEqual(got, want any) bool {
	// Repeated flag case: fixture declares a list, actual is the raw []any.
	if wantList, ok := want.([]any); ok {
		gotList, ok := got.([]any)
		if !ok {
			return false
		}
		if len(gotList) != len(wantList) {
			return false
		}
		for i := range gotList {
			if !scalarEqual(gotList[i], wantList[i]) {
				return false
			}
		}
		return true
	}
	// Single-value case: unwrap the []any wrapper and compare scalars.
	if gotList, ok := got.([]any); ok && len(gotList) == 1 {
		return scalarEqual(gotList[0], want)
	}
	return scalarEqual(got, want)
}

// scalarEqual compares two scalar values, treating int / int64 / float64
// equivalently when their numeric values match (YAML decodes int into int,
// JSON decodes int into float64; the coerce layer produces both).
func scalarEqual(got, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	// Numeric coercion: bridge int/int64/float64 for the common spec types.
	gn, gok := asFloat64(got)
	wn, wok := asFloat64(want)
	if gok && wok {
		return gn == wn
	}
	return false
}

func asFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// stdinEqual compares the actual Result.Stdin to the fixture's expected
// stdin. nil/absent match. For raw bytes, the fixture states the expected
// content as a string; we compare via string(got). For structured maps,
// nested values are compared with the int/float bridge applied (JSON
// decodes integers into float64; YAML decodes them into int).
func stdinEqual(got, want any) bool {
	if got == nil && want == nil {
		return true
	}
	if gotBytes, ok := got.([]byte); ok {
		if wantStr, ok := want.(string); ok {
			return string(gotBytes) == wantStr
		}
	}
	return deepEqualWithNumericBridge(got, want)
}

// deepEqualWithNumericBridge recursively compares two values using
// scalarEqual at leaves (so int(42) == float64(42)) and structural
// recursion on maps and slices.
func deepEqualWithNumericBridge(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		if len(g) != len(w) {
			return false
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				return false
			}
			if !deepEqualWithNumericBridge(gv, wv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok {
			return false
		}
		if len(g) != len(w) {
			return false
		}
		for i := range w {
			if !deepEqualWithNumericBridge(g[i], w[i]) {
				return false
			}
		}
		return true
	default:
		return scalarEqual(got, want)
	}
}

// Sanity check that we actually loaded fixtures and didn't silently skip
// the entire conformance suite. errors.Is import retained for parity with
// other test files when we add error-type assertions later.
var _ = errors.Is
